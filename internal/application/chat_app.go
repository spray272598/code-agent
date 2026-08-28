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
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	mcpmodel "github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/slash"
	sshmodel "github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
	"github.com/spray272598/code-agent/internal/observability"
)

type ChatApp struct {
	loop         engine.Runner
	sessions     sessrepo.ISessionRepository
	messages     sessrepo.IMessageRepository
	tools        *tool.MapRegistry
	perm         *security.Guard
	redis        *redisx.Client
	skills       *skill.Service
	slash        *slash.Registry
	memSvc       *memory.Service
	auditRepo    audit.Repository
	llmKeyRepo   llmkey.Repository
	blobs        blob.Store
	summaryRepo  sessrepo.ISummaryRepository
	workspace    string
	keys         *auth.KeyStore
	authSvc      *AuthService
	tokenSvc     *TokenService
	deviceSvc    *DeviceService
	mcpFactory   mcpport.IUserMCPManagerFactory

	// Extracted services
	cp       *CheckpointService
	idemSvc  *IdempotencyService
	rateSvc  *RateQuotaService
	sshSvc   *SSHService
	mcpFace  *MCPFacade
}

// Set* methods retained for gradual migration; prefer application.Option.
func (a *ChatApp) SetSkills(s *skill.Service)    { a.skills = s }
func (a *ChatApp) SetMemory(s *memory.Service)   { a.memSvc = s }
func (a *ChatApp) SetAudit(r audit.Repository)   { a.auditRepo = r }
func (a *ChatApp) SetLLMKey(r llmkey.Repository) { a.llmKeyRepo = r }
func (a *ChatApp) SetBlobStore(s blob.Store)     { a.blobs = s }

// MCP delegation methods
func (a *ChatApp) SetMCPFactory(f mcpport.IUserMCPManagerFactory) {
	a.mcpFactory = f
	a.mcpFace = &MCPFacade{factory: f}
}
func (a *ChatApp) MCPFactory() mcpport.IUserMCPManagerFactory { return a.mcpFactory }
func (a *ChatApp) MCPFor(ctx context.Context) (mcpport.IMCPManagerPort, error) {
	if a.mcpFace == nil {
		return nil, fmt.Errorf("mcp factory not configured")
	}
	return a.mcpFace.MCPFor(ctx)
}

// SetAuthService injects the multi-tenant auth service (Sprint 1.2).
func (a *ChatApp) SetAuthService(s *AuthService) { a.authSvc = s }

// AuthService returns the multi-tenant auth service, or nil if not configured.
func (a *ChatApp) AuthService() *AuthService { return a.authSvc }

// SetTokenService injects the JWT/refresh token service (Sprint 1.3).
func (a *ChatApp) SetTokenService(s *TokenService) { a.tokenSvc = s }

// TokenService returns the token service, or nil if not configured.
func (a *ChatApp) TokenService() *TokenService { return a.tokenSvc }

// SetDeviceService injects the RFC8628 device authorization service (Sprint 1.4).
func (a *ChatApp) SetDeviceService(s *DeviceService) { a.deviceSvc = s }

// DeviceService returns the device authorization service, or nil if not configured.
func (a *ChatApp) DeviceService() *DeviceService { return a.deviceSvc }

// SSH delegation methods
func (a *ChatApp) SetSSH(pool *sshinfra.Pool, repo sshport.IConnectionRepository) {
	a.sshSvc = &SSHService{pool: pool, repo: repo}
}
func (a *ChatApp) SSHPool() *sshinfra.Pool {
	if a.sshSvc == nil {
		return nil
	}
	return a.sshSvc.pool
}
func (a *ChatApp) InstallSSH(ctx context.Context, cfg sshmodel.ConnectionConfig) error {
	if a.sshSvc == nil {
		return fmt.Errorf("ssh disabled")
	}
	return a.sshSvc.InstallSSH(ctx, cfg)
}
func (a *ChatApp) ListSSHConnections(ctx context.Context) ([]*sshmodel.ConnectionConfig, error) {
	if a.sshSvc == nil {
		return nil, fmt.Errorf("ssh disabled")
	}
	return a.sshSvc.ListSSHConnections(ctx)
}
func (a *ChatApp) DeleteSSHConnection(ctx context.Context, id string) error {
	if a.sshSvc == nil {
		return fmt.Errorf("ssh disabled")
	}
	return a.sshSvc.DeleteSSHConnection(ctx, id)
}
func (a *ChatApp) SSHHealth() []sshmodel.HealthStatus {
	if a.sshSvc == nil {
		return nil
	}
	return a.sshSvc.SSHHealth()
}
func (a *ChatApp) Slash() *slash.Registry    { return a.slash }
func (a *ChatApp) Skills() *skill.Service    { return a.skills }
func (a *ChatApp) Memory() *memory.Service   { return a.memSvc }
func (a *ChatApp) Audit() audit.Repository   { return a.auditRepo }
func (a *ChatApp) Blobs() blob.Store         { return a.blobs }
func (a *ChatApp) LLMKey() llmkey.Repository { return a.llmKeyRepo }

func (a *ChatApp) GetBlob(ctx context.Context, key string) ([]byte, error) {
	if a.blobs == nil {
		return nil, fmt.Errorf("blob store disabled")
	}
	return a.blobs.Get(ctx, key)
}

func (a *ChatApp) ListAudit(ctx context.Context, userID, sessionID string, limit int) ([]audit.Entry, error) {
	if a.auditRepo == nil {
		return nil, nil
	}
	return a.auditRepo.ListBySession(ctx, userID, sessionID, limit)
}

// ListAuditCtx is the ctx-driven (Sprint 1.6) form: the userID is taken from
// tenant.From(ctx). Use this from HTTP handlers that already passed through
// authJWT so the principal's userID is the only valid filter.
func (a *ChatApp) ListAuditCtx(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	if a.auditRepo == nil {
		return nil, nil
	}
	return a.auditRepo.ListForUser(ctx, sessionID, limit)
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
	if a.mcpFactory == nil {
		return fmt.Errorf("mcp disabled")
	}
	mgr, err := a.mcpFactory.For(ctx)
	if err != nil {
		return fmt.Errorf("mcp tenant: %w", err)
	}
	if transport == "" {
		transport = "stdio"
	}
	if timeout <= 0 {
		timeout = 60
	}
	return mgr.AddOrUpdate(ctx, mcpmodel.ServerConfig{
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
	SessionID      string `json:"sessionId"`
	UserID         string `json:"userId"`
	ProjectID      string `json:"projectId"`
	Message        string `json:"message"`
	AutoApprove    bool   `json:"autoApprove"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
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
		ListMCP: func(sctx slash.Context) string {
			if a.mcpFactory == nil {
				return "(mcp disabled)"
			}
			// ListMCP is the per-user view from within the agent session. The
			// slash handler context carries the session's userID; build a tenant
			// ctx and let the factory resolve this user's Manager.
			if sctx.UserID == "" {
				return "(no session)"
			}
			mgr, err := a.mcpFactory.ForUserID(sctx.UserID)
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
	ttl := time.Duration(a.cp.timeoutSec)*time.Second + 15*time.Second
	ok, err := a.redis.TryLock(ctx, "run:lock:"+sessionID, val, ttl)
	if err != nil {
		// lock errors must not block runs
		return func() {}, nil
	}
	if !ok {
		return nil, fmt.Errorf("session %s is already running", sessionID)
	}
	return func() {
		_ = a.redis.Unlock(context.Background(), "run:lock:"+sessionID, val)
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.cp.timeoutSec)*time.Second)
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
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(a.cp.timeoutSec)*time.Second)
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
							UserID:     session.UserID,
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

// Checkpoint delegation
func (a *ChatApp) SetHooks(h *hook.Bus)                              { a.cp.SetHooks(h) }
func (a *ChatApp) GetCheckpoint(ctx context.Context, sid string) (*checkpoint.Snapshot, error) {
	return a.cp.GetCheckpoint(ctx, sid)
}
func (a *ChatApp) ListCheckpoints(ctx context.Context, status string, limit int) ([]*checkpoint.Snapshot, error) {
	return a.cp.ListCheckpoints(ctx, status, limit)
}
func (a *ChatApp) IsSessionRunning(sid string) bool   { return a.cp.IsSessionRunning(sid) }
func (a *ChatApp) ActiveRuns() []string              { return a.cp.ActiveRuns() }
func (a *ChatApp) SendControl(sid string, sig engine.ControlSignal, goal string) bool {
	return a.cp.SendControl(sid, sig, goal)
}
func (a *ChatApp) ReplanSession(sid, newGoal string) bool { return a.cp.ReplanSession(sid, newGoal) }
func (a *ChatApp) PauseSession(sid string) bool           { return a.cp.PauseSession(sid) }
func (a *ChatApp) ResumeControl(sid string) bool          { return a.cp.ResumeControl(sid) }
func (a *ChatApp) InterruptSession(sid, reason string) (bool, error) {
	return a.cp.InterruptSession(sid, reason)
}
func (a *ChatApp) EnterPlanMode(sid string) bool  { return a.cp.EnterPlanMode(sid) }
func (a *ChatApp) ExitPlanMode(sid string) bool   { return a.cp.ExitPlanMode(sid) }
func (a *ChatApp) CancelSession(sid, reason string) (bool, error) {
	return a.cp.CancelSession(sid, reason)
}
func (a *ChatApp) ListResumable(ctx context.Context) []*checkpoint.Snapshot {
	return a.cp.ListResumable(ctx)
}
func (a *ChatApp) RestoreCheckpoints(ctx context.Context) (int, error) {
	return a.cp.RestoreCheckpoints(ctx)
}

// ResumeSession continues an interrupted run, injecting step/tool context so the
// agent can pick up near the break point instead of redoing completed steps.
func (a *ChatApp) ResumeSession(ctx context.Context, sessionID, message string) (*ChatResponse, error) {
	snap, err := a.GetCheckpoint(ctx, sessionID)
	if err != nil || snap == nil {
		return nil, fmt.Errorf("no resumable checkpoint for session %s", sessionID)
	}
	hint := ""
	if snap.Step > 0 {
		hint += fmt.Sprintf("Last interrupted at step %d", snap.Step)
	}
	if t, ok := snap.Meta["lastTool"]; ok {
		if hint != "" {
			hint += ", "
		}
		hint += fmt.Sprintf("last executed tool: %v", t)
	}
	if hint != "" {
		hint += "."
	}
	if strings.TrimSpace(message) == "" {
		message = "Continue."
	}
	if hint != "" {
		message = message + "\n[Checkpoint context] " + hint + " Please continue unfinished work from the checkpoint, do not repeat completed steps."
	}
	req := ChatRequest{
		SessionID: sessionID, UserID: snap.UserID, ProjectID: snap.ProjectID,
		Message: message,
	}
	return a.Chat(req)
}

func (a *ChatApp) markRun(session *sessmodel.Session, req ChatRequest, status string, pending *security.PendingConfirm, errClass string) {
	a.cp.markRun(session, req, status, pending, errClass)
}

func (a *ChatApp) persistResultCheckpoint(session *sessmodel.Session, req ChatRequest, res *engine.Result, ctxErr error) {
	a.cp.persistResultCheckpoint(session, req, res, ctxErr)
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

// Rate/quota delegation
func (a *ChatApp) checkRate(ctx context.Context, userID string) error { return a.rateSvc.checkRate(ctx, userID) }
func (a *ChatApp) checkQuota(ctx context.Context, userID string) error { return a.rateSvc.checkQuota(ctx, userID) }
func (a *ChatApp) UsageSnapshot(ctx context.Context, userID, sessionID string) *Usage {
	return a.rateSvc.UsageSnapshot(ctx, userID, sessionID)
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}
