package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	mcpmodel "github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/observability"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/slash"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
)

type ChatApp struct {
	loop        engine.Runner // native Loop or Eino orchestrator
	sessions    sessrepo.ISessionRepository
	messages    sessrepo.IMessageRepository
	tools       *tool.MapRegistry
	perm        *security.Guard
	redis       *redisx.Client
	skills      *skill.Service
	slash       *slash.Registry
	mcp         mcpport.IMCPManagerPort
	memSvc      *memory.Service
	auditRepo   audit.Repository
	blobs       blob.Store
	ckStore     checkpoint.Store
	runs        *checkpoint.RunRegistry
	timeoutSec  int
	workspace   string
	rateEnabled bool
	ratePerMin  int
	keys        *auth.KeyStore
}

// Set* methods retained for gradual migration; prefer application.Option.
func (a *ChatApp) SetSkills(s *skill.Service)      { a.skills = s }
func (a *ChatApp) SetMCP(m mcpport.IMCPManagerPort) { a.mcp = m }
func (a *ChatApp) SetMemory(s *memory.Service)     { a.memSvc = s }
func (a *ChatApp) SetAudit(r audit.Repository)     { a.auditRepo = r }
func (a *ChatApp) SetBlobStore(s blob.Store)       { a.blobs = s }
func (a *ChatApp) Slash() *slash.Registry      { return a.slash }
func (a *ChatApp) Skills() *skill.Service      { return a.skills }
func (a *ChatApp) MCP() mcpport.IMCPManagerPort { return a.mcp }
func (a *ChatApp) Memory() *memory.Service     { return a.memSvc }
func (a *ChatApp) Audit() audit.Repository     { return a.auditRepo }
func (a *ChatApp) Blobs() blob.Store           { return a.blobs }

func (a *ChatApp) GetBlob(ctx context.Context, key string) ([]byte, error) {
	if a.blobs == nil {
		return nil, fmt.Errorf("blob store disabled")
	}
	return a.blobs.Get(ctx, key)
}

func (a *ChatApp) ListAudit(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	if a.auditRepo == nil {
		return nil, nil
	}
	return a.auditRepo.ListBySession(ctx, sessionID, limit)
}

// SaveMemory API/helper
func (a *ChatApp) SaveMemory(ctx context.Context, item *memport.MemoryItem) error {
	if a.memSvc == nil {
		return fmt.Errorf("memory disabled")
	}
	return a.memSvc.Save(ctx, item)
}

func (a *ChatApp) ListMemory(ctx context.Context, userID, projectID, scope string, limit int) ([]memport.MemoryItem, error) {
	if a.memSvc == nil {
		return nil, nil
	}
	return a.memSvc.List(ctx, userID, projectID, memport.Scope(scope), limit)
}

func (a *ChatApp) SearchMemory(ctx context.Context, userID, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	if a.memSvc == nil {
		return nil, nil
	}
	return a.memSvc.Search(ctx, userID, projectID, query, limit)
}

// InstallMCP installs/updates an MCP server via domain port.
func (a *ChatApp) InstallMCP(ctx context.Context, name, transport, command string, args []string, env map[string]string, url string, enabled bool, timeout int) error {
	if a.mcp == nil {
		return fmt.Errorf("mcp disabled")
	}
	if transport == "" {
		transport = "stdio"
	}
	if timeout <= 0 {
		timeout = 60
	}
	return a.mcp.AddOrUpdate(ctx, mcpmodel.ServerConfig{
		Name: name, Transport: transport, Command: command, Args: args,
		Env: env, URL: url, Enabled: enabled, TimeoutSec: timeout,
	})
}

// Auth verifies API key via SHA-256 hash + constant-time compare (never stores plaintext after boot).
func (a *ChatApp) Auth(apiKey string) bool {
	if a.keys == nil || a.keys.Empty() {
		return true // dev open when no keys configured
	}
	return a.keys.Valid(apiKey)
}

func (a *ChatApp) CreateSession(userID, projectID, title string) (*sessmodel.Session, error) {
	if userID == "" {
		userID = "anonymous"
	}
	s := sessmodel.NewSession(newID("sess"), userID, projectID, title, a.workspace)
	if err := a.sessions.Save(context.Background(), s); err != nil {
		return nil, err
	}
	return s, nil
}

func (a *ChatApp) GetSession(id string) (*sessmodel.Session, error) {
	return a.sessions.FindByID(context.Background(), id)
}

func (a *ChatApp) ListSessions(userID string) ([]*sessmodel.Session, error) {
	return a.sessions.ListByUser(context.Background(), userID, 50)
}

func (a *ChatApp) ListTools() []map[string]string {
	return a.tools.Descriptions()
}

func (a *ChatApp) Permission() *security.Guard { return a.perm }

type ChatRequest struct {
	SessionID   string `json:"sessionId"`
	UserID      string `json:"userId"`
	ProjectID   string `json:"projectId"`
	Message     string `json:"message"`
	AutoApprove bool   `json:"autoApprove"`
}

type ChatResponse struct {
	SessionID      string `json:"sessionId"`
	Response       string `json:"response"`
	Steps          int    `json:"steps"`
	ToolCalls      int    `json:"toolCalls"`
	TokenUsed      int    `json:"tokenUsed"`
	NeedPermission bool   `json:"needPermission,omitempty"`
	Pending        any    `json:"pendingPermission,omitempty"`
	ErrorClass     string `json:"errorClass,omitempty"`
	Slash          bool   `json:"slash,omitempty"`
}

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
		ListMCP: func() string {
			if a.mcp == nil {
				return "(mcp disabled)"
			}
			hs := a.mcp.Health(context.Background())
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

func (a *ChatApp) Chat(req ChatRequest) (*ChatResponse, error) {
	forceCompact := false
	if resp, handled, fc := a.trySlash(&req); handled {
		return resp, nil
	} else {
		forceCompact = fc
	}

	session, err := a.resolveSession(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.timeoutSec)*time.Second)
	defer cancel()
	if err := a.checkRate(ctx, req.UserID); err != nil {
		return nil, err
	}
	if a.redis != nil && a.redis.Enabled() {
		if err := a.redis.Set(ctx, "sess:hot:"+session.ID, req.Message, 24*time.Hour); err != nil {
			observability.Warnf("sess hot set: %v", err)
		}
	}
	observability.Current().AddChatTotal(1)
	runCtx, runCancel := context.WithCancel(ctx)
	if a.runs != nil {
		a.runs.Register(session.ID, runCancel)
		defer a.runs.Unregister(session.ID, runCancel)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	res, err := a.loop.Run(runCtx, session, req.Message, nil, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact})
	if err != nil && res == nil {
		observability.Current().AddChatErrors(1)
		if runCtx.Err() != nil {
			a.markRun(session, req, checkpoint.StatusCancelled, nil, "cancel")
			return &ChatResponse{SessionID: session.ID, Response: "cancelled", ErrorClass: "cancel"}, nil
		}
		a.markRun(session, req, checkpoint.StatusFailed, nil, "error")
		return nil, err
	}
	if res == nil {
		observability.Current().AddChatErrors(1)
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
	return &ChatResponse{
		SessionID: res.SessionID, Response: res.Response, Steps: res.Steps,
		ToolCalls: res.ToolCalls, TokenUsed: res.TokenUsed,
		NeedPermission: res.NeedPermission, Pending: res.Pending, ErrorClass: res.ErrorClass,
	}, nil
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
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(a.timeoutSec)*time.Second)
	if err := a.checkRate(ctx, req.UserID); err != nil {
		cancel()
		return nil, nil, err
	}
	if a.runs != nil {
		a.runs.Register(session.ID, cancel)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	ch := make(chan *engine.Event, 128)
	go func() {
		defer close(ch)
		defer cancel()
		if a.runs != nil {
			defer a.runs.Unregister(session.ID, cancel)
		}
		res, err := a.loop.Run(ctx, session, req.Message, ch, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact})
		if err != nil && res == nil {
			if ctx.Err() != nil {
				a.markRun(session, req, checkpoint.StatusCancelled, nil, "cancel")
				select {
				case ch <- &engine.Event{Type: engine.EventCancel, Content: "cancelled", Completed: true, Timestamp: time.Now().UnixMilli()}:
				default:
				}
			} else {
				a.markRun(session, req, checkpoint.StatusFailed, nil, "error")
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

// CancelSession interrupts an in-process run and writes a cancelled checkpoint.
func (a *ChatApp) CancelSession(sessionID, reason string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("sessionId required")
	}
	ok := false
	if a.runs != nil {
		ok = a.runs.Cancel(sessionID)
	}
	if a.ckStore != nil {
		snap := &checkpoint.Snapshot{
			SessionID: sessionID, Status: checkpoint.StatusCancelled,
			ErrorClass: "cancel", Meta: map[string]any{"reason": reason, "hadActive": ok},
			UpdatedAt: time.Now(), CreatedAt: time.Now(),
		}
		if prev, _ := a.ckStore.Get(context.Background(), sessionID); prev != nil {
			snap.Goal = prev.Goal
			snap.LastInput = prev.LastInput
			snap.UserID = prev.UserID
			snap.CreatedAt = prev.CreatedAt
		}
		if err := a.ckStore.Save(context.Background(), snap); err != nil {
			observability.LogError("checkpoint save cancel", err)
		}
	}
	return ok, nil
}

// GetCheckpoint returns durable snapshot for a session.
func (a *ChatApp) GetCheckpoint(ctx context.Context, sessionID string) (*checkpoint.Snapshot, error) {
	if a.ckStore == nil {
		return nil, fmt.Errorf("checkpoint store disabled")
	}
	return a.ckStore.Get(ctx, sessionID)
}

// ListCheckpoints lists durable snapshots (optional status filter).
func (a *ChatApp) ListCheckpoints(ctx context.Context, status string, limit int) ([]*checkpoint.Snapshot, error) {
	if a.ckStore == nil {
		return nil, fmt.Errorf("checkpoint store disabled")
	}
	return a.ckStore.List(ctx, status, limit)
}

// IsSessionRunning reports in-process active agent loop.
func (a *ChatApp) IsSessionRunning(sessionID string) bool {
	if a.runs == nil {
		return false
	}
	return a.runs.IsRunning(sessionID)
}

// ActiveRuns lists session IDs with in-process runs.
func (a *ChatApp) ActiveRuns() []string {
	if a.runs == nil {
		return nil
	}
	return a.runs.Active()
}

// RestoreCheckpoints rehydrates pending confirms from durable store (process start).
func (a *ChatApp) RestoreCheckpoints(ctx context.Context) (int, error) {
	if a.ckStore == nil || a.perm == nil {
		return 0, nil
	}
	list, err := a.ckStore.List(ctx, checkpoint.StatusInterrupt, 200)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range list {
		if s == nil || s.Pending == nil {
			continue
		}
		p := &security.PendingConfirm{
			ID: s.Pending.ID, SessionID: s.Pending.SessionID, Tool: s.Pending.Tool,
			Args: s.Pending.Args, Reason: s.Pending.Reason, RuleID: s.Pending.RuleID,
			Layer: s.Pending.Layer, CreatedAt: s.Pending.CreatedAt,
		}
		a.perm.RestorePending(p)
		n++
	}
	return n, nil
}

func (a *ChatApp) markRun(session *sessmodel.Session, req ChatRequest, status string, pending *security.PendingConfirm, errClass string) {
	if a.ckStore == nil || session == nil {
		return
	}
	snap := &checkpoint.Snapshot{
		SessionID: session.ID, UserID: session.UserID, ProjectID: session.ProjectID,
		Status: status, Goal: req.Message, LastInput: req.Message, ErrorClass: errClass,
		UpdatedAt: time.Now(), CreatedAt: time.Now(),
	}
	if pending != nil {
		snap.Pending = &checkpoint.PendingTool{
			ID: pending.ID, SessionID: pending.SessionID, Tool: pending.Tool, Args: pending.Args,
			Reason: pending.Reason, RuleID: pending.RuleID, Layer: pending.Layer, CreatedAt: pending.CreatedAt,
		}
	}
	if prev, err := a.ckStore.Get(context.Background(), session.ID); err != nil {
		observability.LogError("checkpoint get for markRun", err)
	} else if prev != nil && !prev.CreatedAt.IsZero() {
		snap.CreatedAt = prev.CreatedAt
	}
	if err := a.ckStore.Save(context.Background(), snap); err != nil {
		observability.LogError("checkpoint save markRun", err)
	}
}

func (a *ChatApp) persistResultCheckpoint(session *sessmodel.Session, req ChatRequest, res *engine.Result, ctxErr error) {
	if a.ckStore == nil || session == nil || res == nil {
		return
	}
	status := checkpoint.StatusCompleted
	if ctxErr != nil {
		status = checkpoint.StatusCancelled
		res.ErrorClass = "cancel"
	} else if res.NeedPermission {
		status = checkpoint.StatusInterrupt
	} else if res.ErrorClass != "" && res.ErrorClass != "permission" {
		status = checkpoint.StatusFailed
	}
	var pend *security.PendingConfirm
	if p, ok := res.Pending.(*security.PendingConfirm); ok {
		pend = p
	} else if res.NeedPermission && a.perm != nil {
		if list := a.perm.ListPending(session.ID); len(list) > 0 {
			pend = list[0]
		}
	}
	a.markRun(session, req, status, pend, res.ErrorClass)
}

func (a *ChatApp) resolveSession(req ChatRequest) (*sessmodel.Session, error) {
	if req.SessionID != "" {
		s, err := a.sessions.FindByID(context.Background(), req.SessionID)
		if err != nil {
			return nil, err
		}
		if s != nil && s.Status == "ACTIVE" {
			return s, nil
		}
	}
	userID := req.UserID
	if userID == "" {
		userID = "anonymous"
	}
	s := sessmodel.NewSession(newID("sess"), userID, req.ProjectID, "auto", a.workspace)
	if err := a.sessions.Save(context.Background(), s); err != nil {
		return nil, err
	}
	return s, nil
}

func (a *ChatApp) checkRate(ctx context.Context, userID string) error {
	if !a.rateEnabled {
		return nil
	}
	if userID == "" {
		userID = "anonymous"
	}
	limit := a.ratePerMin
	if limit <= 0 {
		limit = 60
	}
	if a.redis == nil {
		return nil
	}
	ok, err := a.redis.AllowRate(ctx, "rl:chat:"+userID, limit, time.Minute)
	if err != nil {
		return nil
	}
	if !ok {
		return fmt.Errorf("rate limit exceeded")
	}
	return nil
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}
