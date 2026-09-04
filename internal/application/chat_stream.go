package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/slash"
	"github.com/spray272598/code-agent/internal/observability"
)

func (a *ChatApp) slashCtx() slash.Context {
	return slash.Context{
		ListTools: a.ListTools,
		ListSkills: func() string {
			if a.skills == nil {
				return "(no skills)"
			}
			list := a.skills.List()
			if len(list) == 0 {
				return "(empty skills dir)"
			}
			var b strings.Builder
			for _, sk := range list {
				b.WriteString(fmt.Sprintf("- %s (%s): %s\n", sk.ID, sk.Name, sk.Description))
			}
			return b.String()
		},
		ListMCP: func(_ slash.Context) string {
			if a.mcpFactory == nil {
				return "(mcp disabled)"
			}
			mgr, err := a.mcpFactory.For(context.Background())
			if err != nil || mgr == nil {
				return "(mcp disabled)"
			}
			hs := mgr.Health(context.Background())
			if len(hs) == 0 {
				return "(no mcp servers installed)"
			}
			var b strings.Builder
			for _, h := range hs {
				on := "off"
				if h.Online {
					on = "online"
				}
				b.WriteString(fmt.Sprintf("- %s [%s] tools=%d %s\n", h.Name, on, h.ToolCount, h.LastError))
			}
			return b.String()
		},
	}
}

func (a *ChatApp) trySlash(req *ChatRequest) (*ChatResponse, bool, bool) {
	// returns (resp, handled, forceCompact) — if rewrite, handled=false forceCompact may set
	if a.slash == nil || !strings.HasPrefix(strings.TrimSpace(req.Message), "/") {
		return nil, false, false
	}
	ctx := a.slashCtx()
	ctx.SessionID = req.SessionID
	ctx.UserID = req.UserID
	res := a.slash.Try(req.Message, ctx)
	if res.Rewrite != "" {
		req.Message = res.Rewrite
		return nil, false, res.ForceCompact
	}
	if !res.Handled {
		return nil, false, res.ForceCompact
	}
	// ensure session id
	sid := req.SessionID
	if sid == "" {
		if s, err := a.CreateSession(req.UserID, req.ProjectID, "slash"); err == nil {
			sid = s.ID
		}
	}
	return &ChatResponse{SessionID: sid, Response: res.Response, Slash: true}, true, false
}

// acquireRunLock prevents the same session from running concurrently across
// multiple instances. Returns a release func; nil error means proceed. Lock
// failures are non-fatal when Redis is absent (single-instance mode).
func (a *ChatApp) acquireRunLock(ctx context.Context, sessionID string) (func(), error) {
	if a.redis == nil || !a.redis.Enabled() || sessionID == "" {
		return func() {}, nil
	}
	val := newID("lock")
	ok, err := a.redis.TryLock(ctx, "run:lock:"+sessionID, val, a.cp.lockTTL())
	if err != nil {
		// lock errors must not block runs
		return func() {}, nil
	}
	if !ok {
		return nil, fmt.Errorf("session %s is already running", sessionID)
	}
	return func() {
		if err := a.redis.Unlock(context.Background(), "run:lock:"+sessionID, val); err != nil {
			slog.Warn("failed to release run lock", "session", sessionID, "error", err)
		}
	}, nil
}

// Idempotency delegation
func (a *ChatApp) checkIdempotency(ctx context.Context, req ChatRequest) (status string, cached *ChatResponse, err error) {
	return a.idemSvc.checkIdempotency(ctx, req, a.redis)
}
func (a *ChatApp) storeIdempotency(ctx context.Context, req ChatRequest, resp *ChatResponse, runErr error) {
	a.idemSvc.storeIdempotency(ctx, req, resp, runErr, a.redis)
}

func (a *ChatApp) Chat(req ChatRequest) (*ChatResponse, error) {
	forceCompact := false
	if resp, handled, fc := a.trySlash(&req); handled {
		return resp, nil
	} else {
		forceCompact = fc
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.cp.runTimeout())
	defer cancel()

	session, err := a.resolveSession(req)
	if err != nil {
		return nil, err
	}
	if err := a.checkRate(ctx, req.UserID); err != nil {
		return nil, err
	}
	if err := a.checkQuota(ctx, req.UserID); err != nil {
		return nil, err
	}

	// --- Idempotency-key deduplication ---
	// A client-supplied key means "this exact request was already issued".
	// Replay a completed result, or reject a still-in-flight duplicate so the
	// agent never runs twice for one logical user action (retries, SSE reconnect).
	// Checked after validation passes so a rejected pre-run check never leaves a
	// stuck PENDING slot.
	if req.IdempotencyKey != "" {
		switch status, cached, ierr := a.checkIdempotency(ctx, req); status {
		case "done":
			return cached, nil
		case "pending":
			return nil, fmt.Errorf("request %s is already in progress", req.IdempotencyKey)
		case "error":
			return nil, ierr
		}
	}

	unlock, err := a.acquireRunLock(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if a.redis != nil && a.redis.Enabled() {
		if err := a.redis.Set(ctx, "sess:hot:"+session.ID, req.Message, 24*time.Hour); err != nil {
			observability.Warnf("sess hot set: %v", err)
		}
	}
	observability.Current().AddChatTotal(1)
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	if a.cp.runs != nil {
		a.cp.runs.Register(session.ID, runCancel)
		defer a.cp.runs.Unregister(session.ID, runCancel)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	res, err := a.loop.Run(runCtx, session, req.Message, nil, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact})
	switch {
	case err != nil && res == nil:
		observability.Current().AddChatErrors(1)
		if runCtx.Err() != nil {
			a.markRun(session, req, checkpoint.StatusCancelled, nil, "cancel")
			cancelResp := &ChatResponse{SessionID: session.ID, Response: "cancelled", ErrorClass: "cancel"}
			a.storeIdempotency(ctx, req, cancelResp, nil)
			return cancelResp, nil
		}
		a.markRun(session, req, checkpoint.StatusFailed, nil, "error")
		a.storeIdempotency(ctx, req, nil, err)
		return nil, err
	case res == nil:
		observability.Current().AddChatErrors(1)
		a.storeIdempotency(ctx, req, nil, fmt.Errorf("empty result"))
		return nil, fmt.Errorf("empty result")
	}
	a.persistResultCheckpoint(session, req, res, runCtx.Err())
	if a.redis != nil && a.redis.Enabled() && res.TokenUsed > 0 {
		day := time.Now().Format("20060102")
		if _, err := a.redis.IncrBy(ctx, fmt.Sprintf("token:user:%s:%s", session.UserID, day), int64(res.TokenUsed), 48*time.Hour); err != nil {
			observability.Warnf("token user incr: %v", err)
		}
		if _, err := a.redis.IncrBy(ctx, "token:sess:"+session.ID, int64(res.TokenUsed), 7*24*time.Hour); err != nil {
			observability.Warnf("token sess incr: %v", err)
		}
	}
	resp := &ChatResponse{
		SessionID: res.SessionID, Response: res.Response, Steps: res.Steps,
		ToolCalls: res.ToolCalls, TokenUsed: res.TokenUsed,
		NeedPermission: res.NeedPermission, Pending: res.Pending, ErrorClass: res.ErrorClass,
	}
	a.storeIdempotency(ctx, req, resp, nil)
	return resp, nil
}

// ChatStream runs the agent and streams events. parentCtx should be the HTTP request
// context so client disconnect cancels the loop (no goroutine leak).
func (a *ChatApp) ChatStream(parentCtx context.Context, req ChatRequest) (<-chan *engine.Event, *sessmodel.Session, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	forceCompact := false
	if resp, handled, fc := a.trySlash(&req); handled {
		ch := make(chan *engine.Event, 4)
		go func() {
			defer close(ch)
			select {
			case ch <- &engine.Event{Type: engine.EventSlash, Content: resp.Response, Completed: true, Timestamp: time.Now().UnixMilli()}:
			case <-parentCtx.Done():
				return
			}
			select {
			case ch <- &engine.Event{Type: engine.EventAnswer, Content: resp.Response, Completed: true, Timestamp: time.Now().UnixMilli()}:
			case <-parentCtx.Done():
				return
			}
			select {
			case ch <- &engine.Event{Type: engine.EventDone, Content: resp.Response, Completed: true, Timestamp: time.Now().UnixMilli()}:
			case <-parentCtx.Done():
			}
		}()
		s := &sessmodel.Session{ID: resp.SessionID}
		return ch, s, nil
	} else {
		forceCompact = fc
	}

	session, err := a.resolveSession(req)
	if err != nil {
		return nil, nil, err
	}
	// nest timeout under request ctx — cancel on client disconnect OR timeout OR CancelSession
	ctx, cancel := context.WithTimeout(parentCtx, a.cp.runTimeout())
	if err := a.checkRate(ctx, req.UserID); err != nil {
		cancel()
		return nil, nil, err
	}
	if err := a.checkQuota(ctx, req.UserID); err != nil {
		cancel()
		return nil, nil, err
	}

	// --- Idempotency-key deduplication ---
	// For streaming we cannot replay the event stream, so a completed key is
	// rejected with a pointer to the non-streaming endpoint; an in-flight key is
	// rejected to avoid a second agent run. The final outcome is still stored so
	// a non-streaming retry with the same key can replay it.
	if req.IdempotencyKey != "" {
		switch status, _, ierr := a.checkIdempotency(parentCtx, req); status {
		case "done":
			cancel()
			return nil, nil, fmt.Errorf("idempotent request %s already completed; use the non-streaming endpoint to fetch the result", req.IdempotencyKey)
		case "pending":
			cancel()
			return nil, nil, fmt.Errorf("request %s is already in progress", req.IdempotencyKey)
		case "error":
			cancel()
			return nil, nil, ierr
		}
	}

	unlock, err := a.acquireRunLock(ctx, session.ID)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	ctrlCh := make(chan engine.Control, 8)
	if a.cp.runs != nil {
		a.cp.runs.Register(session.ID, cancel)
		a.cp.runs.AttachControl(session.ID, ctrlCh)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	ch := make(chan *engine.Event, 128)
	go func() {
		defer close(ch)
		defer cancel()
		defer unlock()
		if a.cp.runs != nil {
			defer a.cp.runs.Unregister(session.ID, cancel)
		}
		res, err := a.loop.Run(ctx, session, req.Message, ch, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact, ControlCh: ctrlCh})
		if err != nil && res == nil {
			if ctx.Err() != nil {
				a.markRun(session, req, checkpoint.StatusCancelled, nil, "cancel")
				a.storeIdempotency(ctx, req, nil, fmt.Errorf("cancelled"))
				select {
				case ch <- &engine.Event{Type: engine.EventCancel, Content: "cancelled", Completed: true, Timestamp: time.Now().UnixMilli()}:
				default:
				}
			} else {
				a.markRun(session, req, checkpoint.StatusFailed, nil, "error")
				a.storeIdempotency(ctx, req, nil, err)
				select {
				case ch <- &engine.Event{Type: engine.EventError, Content: err.Error(), Completed: true, Timestamp: time.Now().UnixMilli()}:
				case <-ctx.Done():
				}
			}
		}
		if res != nil {
			a.persistResultCheckpoint(session, req, res, ctx.Err())
			if res.NeedPermission {
				select {
				case ch <- &engine.Event{Type: engine.EventCheckpoint, SubType: checkpoint.StatusInterrupt, Content: "checkpoint saved", Data: res.Pending, Timestamp: time.Now().UnixMilli()}:
				default:
				}
			}
			a.storeIdempotency(ctx, req, &ChatResponse{
				SessionID: res.SessionID, Response: res.Response, Steps: res.Steps,
				ToolCalls: res.ToolCalls, TokenUsed: res.TokenUsed,
				NeedPermission: res.NeedPermission, Pending: res.Pending, ErrorClass: res.ErrorClass,
			}, nil)
		}
		if res != nil && a.redis != nil && a.redis.Enabled() && res.TokenUsed > 0 {
			day := time.Now().Format("20060102")
			if _, err := a.redis.IncrBy(ctx, fmt.Sprintf("token:user:%s:%s", session.UserID, day), int64(res.TokenUsed), 48*time.Hour); err != nil {
				observability.Warnf("token incr: %v", err)
			}
		}
	}()
	return ch, session, nil
}

// RunBackground starts a long-running agent task detached from the HTTP
// connection (headless mode). It returns immediately with the session ID; the
// loop runs in a goroutine and can be controlled via SendControl / CancelSession.
// Results and checkpoints are persisted like a normal run, so the caller can
// poll session status later.
func (a *ChatApp) RunBackground(ctx context.Context, req ChatRequest, onEvent func(*engine.Event)) (string, error) {
	if a.loop == nil {
		return "", fmt.Errorf("agent not configured")
	}
	forceCompact := false
	if _, handled, fc := a.trySlash(&req); !handled {
		forceCompact = fc
	}
	ctx, cancel := context.WithCancel(ctx)
	session, err := a.resolveSession(req)
	if err != nil {
		cancel()
		return "", err
	}
	if err := a.checkRate(ctx, req.UserID); err != nil {
		cancel()
		return "", err
	}
	if err := a.checkQuota(ctx, req.UserID); err != nil {
		cancel()
		return "", err
	}
	unlock, err := a.acquireRunLock(ctx, session.ID)
	if err != nil {
		cancel()
		return "", err
	}
	ctrlCh := make(chan engine.Control, 8)
	if a.cp.runs != nil {
		a.cp.runs.Register(session.ID, cancel)
		a.cp.runs.AttachControl(session.ID, ctrlCh)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	go func() {
		defer cancel()
		defer unlock()
		if a.cp.runs != nil {
			defer a.cp.runs.Unregister(session.ID, cancel)
		}
		ch := make(chan *engine.Event, 128)
		go func() {
			for ev := range ch {
				// Long-task cross-segment memory solidification: every time a
				// rolling summary is produced (EventCompress) or the run ends
				// (EventDone), capture the current session summary into the
				// long-term memory store. New segments auto-recall it via
				// memory.FormatForPrompt, so repeated compaction never loses
				// the "what's already been done" context. See plan item M5.7-2.
				if a.summaryRepo != nil && a.memSvc != nil &&
					(ev.Type == engine.EventCompress || ev.Type == engine.EventDone) {
					if s, gerr := a.summaryRepo.Get(ctx, session.ID); gerr == nil && s != "" {
					_ = a.memSvc.Save(ctx, &memport.MemoryItem{
						ProjectID:  session.ProjectID,
						Scope:      memport.ScopeProject,
						Content:    s,
						Category:   "task_progress",
						Importance: 60,
					})
					}
				}
				if onEvent != nil {
					onEvent(ev)
				}
			}
		}()
		res, err := a.loop.Run(ctx, session, req.Message, ch, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact, ControlCh: ctrlCh})
		close(ch)
		if err != nil && res == nil {
			if ctx.Err() != nil {
				a.markRun(session, req, checkpoint.StatusCancelled, nil, "cancel")
			} else {
				a.markRun(session, req, checkpoint.StatusFailed, nil, "error")
			}
		}
		if res != nil {
			a.persistResultCheckpoint(session, req, res, ctx.Err())
		}
		if res != nil && a.redis != nil && a.redis.Enabled() && res.TokenUsed > 0 {
			day := time.Now().Format("20060102")
			if _, err := a.redis.IncrBy(ctx, fmt.Sprintf("token:user:%s:%s", session.UserID, day), int64(res.TokenUsed), 48*time.Hour); err != nil {
				observability.Warnf("token incr: %v", err)
			}
		}
	}()
	return session.ID, nil
}
