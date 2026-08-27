package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	loop         engine.Runner // native Loop or Eino orchestrator
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
	ckStore      checkpoint.Store
	runs         *checkpoint.RunRegistry
	summaryRepo  sessrepo.ISummaryRepository
	hooks        *hook.Bus
	timeoutSec   int
	workspace    string
	rateEnabled  bool
	ratePerMin   int
	quotaEnabled bool
	quotaPerDay  int
	keys         *auth.KeyStore
	sshPool      *sshinfra.Pool
	sshRepo      sshport.IConnectionRepository
	authSvc      *AuthService
	tokenSvc     *TokenService
	deviceSvc    *DeviceService
	// Sprint 1.6: MCP is now per-user. ChatApp no longer holds a single global
	// Manager; callers obtain the per-user Manager via MCPFactory().For(ctx).
	mcpFactory mcpport.IUserMCPManagerFactory
	// idem is the idempotency-key store. Production uses *redisx.Client; tests
	// inject an in-memory fake. Lazily resolved via getIdemStore().
	idem idemStore
}

// Set* methods retained for gradual migration; prefer application.Option.
func (a *ChatApp) SetSkills(s *skill.Service)    { a.skills = s }
func (a *ChatApp) SetMemory(s *memory.Service)   { a.memSvc = s }
func (a *ChatApp) SetAudit(r audit.Repository)   { a.auditRepo = r }
func (a *ChatApp) SetLLMKey(r llmkey.Repository) { a.llmKeyRepo = r }
func (a *ChatApp) SetBlobStore(s blob.Store)     { a.blobs = s }

// SetMCPFactory injects the per-user MCP factory (Sprint 1.6). After this is
// called ChatApp no longer accepts direct Manager wiring; the factory is the
// only way to obtain a Manager for a given tenant.
func (a *ChatApp) SetMCPFactory(f mcpport.IUserMCPManagerFactory) { a.mcpFactory = f }

// MCPFactory returns the per-user MCP factory (never nil if SetMCPFactory
// was called by bootstrap).
func (a *ChatApp) MCPFactory() mcpport.IUserMCPManagerFactory { return a.mcpFactory }

// MCPFor is a convenience: returns the per-user Manager for the tenant on ctx.
// Equivalent to MCPFactory().For(ctx). Callers MUST stamp the asserted userID
// on ctx via mcp.WithAssertedUser before invoking CallTool on the returned
// manager.
func (a *ChatApp) MCPFor(ctx context.Context) (mcpport.IMCPManagerPort, error) {
	if a.mcpFactory == nil {
		return nil, fmt.Errorf("mcp factory not configured")
	}
	return a.mcpFactory.For(ctx)
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

// SetSSH injects SSH pool and repository.
func (a *ChatApp) SetSSH(pool *sshinfra.Pool, repo sshport.IConnectionRepository) {
	a.sshPool = pool
	a.sshRepo = repo
}

// SSHPool returns the SSH connection pool.
func (a *ChatApp) SSHPool() *sshinfra.Pool   { return a.sshPool }
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

// InstallSSH saves SSH connection config and connects.
func (a *ChatApp) InstallSSH(ctx context.Context, cfg sshmodel.ConnectionConfig) error {
	if a.sshPool == nil || a.sshRepo == nil {
		return fmt.Errorf("ssh disabled")
	}
	if cfg.ID == "" {
		cfg.ID = newID("ssh")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "password"
	}
	if err := a.sshRepo.Save(ctx, &cfg); err != nil {
		return err
	}
	if cfg.Enabled {
		return a.sshPool.Connect(ctx, cfg)
	}
	return nil
}

// ListSSHConnections lists all saved SSH connections.
func (a *ChatApp) ListSSHConnections(ctx context.Context) ([]*sshmodel.ConnectionConfig, error) {
	if a.sshRepo == nil {
		return nil, fmt.Errorf("ssh disabled")
	}
	return a.sshRepo.List(ctx)
}

// DeleteSSHConnection removes an SSH connection.
func (a *ChatApp) DeleteSSHConnection(ctx context.Context, id string) error {
	if a.sshPool == nil || a.sshRepo == nil {
		return fmt.Errorf("ssh disabled")
	}
	cfg, err := a.sshRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if cfg != nil {
		_ = a.sshPool.Disconnect(cfg.Name)
	}
	return a.sshRepo.Delete(ctx, id)
}

// SSHHealth returns health status of all SSH connections.
func (a *ChatApp) SSHHealth() []sshmodel.HealthStatus {
	if a.sshPool == nil {
		return nil
	}
	return a.sshPool.Health()
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
	ttl := time.Duration(a.timeoutSec)*time.Second + 15*time.Second
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

// idemStore is the minimal redis contract ChatApp needs for idempotency-key
// deduplication. *redisx.Client satisfies it (Get/Set/TryReserve); tests inject
// an in-memory fake so the logic runs without a live Redis.
type idemStore interface {
	Get(ctx context.Context, key string) (string, error)
	TryReserve(ctx context.Context, key, val string, ttl time.Duration) (bool, error)
	Set(ctx context.Context, key, val string, ttl time.Duration) error
}

// idemWindow is how long a completed idempotency result stays replayable.
const idemWindow = 10 * time.Minute

// getIdemStore returns the configured idempotency store, falling back to the
// live Redis client when present. Returns nil when no store is available
// (Redis disabled) — callers then skip dedup without blocking the run.
func (a *ChatApp) getIdemStore() idemStore {
	if a.idem != nil {
		return a.idem
	}
	if a.redis != nil && a.redis.Enabled() {
		return a.redis
	}
	return nil
}

func idemKey(userID, key string) string {
	if userID == "" {
		return "idem:" + key
	}
	return "idem:" + userID + ":" + key
}

// checkIdempotency inspects an incoming idempotency key and returns the
// resolution status plus (for "done") the cached response to replay.
//
//	"none"    – key unseen; caller should proceed (a PENDING slot is reserved)
//	"pending" – another request with this key is in flight → reject
//	"done"    – completed; cached holds the result to replay
//	"error"   – completed with error; err holds the original error
//
// A store read/lock failure degrades to "none" so the run is never blocked.
func (a *ChatApp) checkIdempotency(ctx context.Context, req ChatRequest) (status string, cached *ChatResponse, err error) {
	if req.IdempotencyKey == "" {
		return "none", nil, nil
	}
	store := a.getIdemStore()
	if store == nil {
		return "none", nil, nil
	}
	key := idemKey(req.UserID, req.IdempotencyKey)
	val, gerr := store.Get(ctx, key)
	if gerr != nil {
		return "none", nil, nil
	}
	if val == "" {
		ok, rerr := store.TryReserve(ctx, key, "pending", idemWindow)
		if rerr != nil {
			return "none", nil, nil
		}
		if !ok {
			// lost the race to a concurrent request → treat as in-flight
			return "pending", nil, nil
		}
		return "none", nil, nil
	}
	if val == "pending" {
		return "pending", nil, nil
	}
	if strings.HasPrefix(val, "done:") {
		var resp ChatResponse
		if jerr := json.Unmarshal([]byte(val[len("done:"):]), &resp); jerr == nil {
			return "done", &resp, nil
		}
		return "none", nil, nil
	}
	if strings.HasPrefix(val, "err:") {
		return "error", nil, errors.New(val[len("err:"):])
	}
	return "none", nil, nil
}

// storeIdempotency records the final outcome of an idempotency-keyed request so
// a retry replays it instead of re-running the agent. runErr != nil stores an
// error result; otherwise resp is stored. No-op when the request has no key or
// no store is configured.
func (a *ChatApp) storeIdempotency(ctx context.Context, req ChatRequest, resp *ChatResponse, runErr error) {
	if req.IdempotencyKey == "" {
		return
	}
	store := a.getIdemStore()
	if store == nil {
		return
	}
	key := idemKey(req.UserID, req.IdempotencyKey)
	if runErr != nil {
		_ = store.Set(ctx, key, "err:"+runErr.Error(), idemWindow)
		return
	}
	if resp != nil {
		if b, jerr := json.Marshal(resp); jerr == nil {
			_ = store.Set(ctx, key, "done:"+string(b), idemWindow)
		}
	}
}

func (a *ChatApp) Chat(req ChatRequest) (*ChatResponse, error) {
	forceCompact := false
	if resp, handled, fc := a.trySlash(&req); handled {
		return resp, nil
	} else {
		forceCompact = fc
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.timeoutSec)*time.Second)
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
	if a.runs != nil {
		a.runs.Register(session.ID, runCancel)
		defer a.runs.Unregister(session.ID, runCancel)
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
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(a.timeoutSec)*time.Second)
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
	if a.runs != nil {
		a.runs.Register(session.ID, cancel)
		a.runs.AttachControl(session.ID, ctrlCh)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	ch := make(chan *engine.Event, 128)
	go func() {
		defer close(ch)
		defer cancel()
		defer unlock()
		if a.runs != nil {
			defer a.runs.Unregister(session.ID, cancel)
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
	if a.runs != nil {
		a.runs.Register(session.ID, cancel)
		a.runs.AttachControl(session.ID, ctrlCh)
	}
	a.markRun(session, req, checkpoint.StatusRunning, nil, "")
	go func() {
		defer cancel()
		defer unlock()
		if a.runs != nil {
			defer a.runs.Unregister(session.ID, cancel)
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

// SendControl delivers a mid-run instruction (replan / pause / resume /
// interrupt) to an active session. It is a no-op if the session is not running
// or the loop was started without a control channel. Returns true if delivered.
func (a *ChatApp) SendControl(sessionID string, sig engine.ControlSignal, goal string) bool {
	if a.runs == nil || sessionID == "" {
		return false
	}
	return a.runs.Control(sessionID, sig, goal)
}

// ReplanSession asks an active session to rebuild its plan from the current
// goal (or a new goal when newGoal is non-empty). This is the user-driven half
// of interruptible re-planning; the loop will emit EventReplan + EventPlanUpdate.
func (a *ChatApp) ReplanSession(sessionID, newGoal string) bool {
	sig := engine.ControlReplan
	if strings.TrimSpace(newGoal) != "" {
		sig = engine.ControlReplanWithGoal
	}
	return a.SendControl(sessionID, sig, strings.TrimSpace(newGoal))
}

// PauseSession asks an active session to pause at the next step boundary and
// emit a checkpoint, awaiting ResumeControl.
func (a *ChatApp) PauseSession(sessionID string) bool {
	return a.SendControl(sessionID, engine.ControlPause, "")
}

// ResumeControl resumes a paused session (control-level resume; distinct from
// the checkpoint ResumeSession(ctx, id, message) that replays a saved run).
func (a *ChatApp) ResumeControl(sessionID string) bool {
	return a.SendControl(sessionID, engine.ControlResume, "")
}

// InterruptSession immediately stops an active session without waiting for a
// step boundary (equivalent to CancelSession).
func (a *ChatApp) InterruptSession(sessionID, reason string) (bool, error) {
	a.SendControl(sessionID, engine.ControlInterrupt, "")
	return a.CancelSession(sessionID, reason)
}

// EnterPlanMode switches an active session into the read-only plan explore
// phase (3.5 PlanMode state machine). The engine's guard denies mutating tools
// until ExitPlanMode is called.
func (a *ChatApp) EnterPlanMode(sessionID string) bool {
	return a.SendControl(sessionID, engine.ControlPlanExplore, "")
}

// ExitPlanMode leaves the plan phase and resumes the writable implement phase.
func (a *ChatApp) ExitPlanMode(sessionID string) bool {
	return a.SendControl(sessionID, engine.ControlPlanImplement, "")
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

// SetHooks injects the lifecycle hook bus and registers a per-step checkpoint
// handler that snapshots agent progress (step count + last tool) to durable store.
// This enables crash/restart resume for non-HITL interruptions.
func (a *ChatApp) SetHooks(h *hook.Bus) {
	a.hooks = h
	if a.hooks == nil || a.ckStore == nil {
		return
	}
	a.hooks.On(hook.PostToolUse, func(ctx context.Context, ev hook.Event) error {
		a.touchStep(ctx, ev.SessionID, ev.Step, ev.Tool)
		return nil
	})
}

// touchStep updates the running snapshot's step/lastTool. It is a no-op unless
// the session currently has a StatusRunning snapshot (i.e. mid-run).
func (a *ChatApp) touchStep(ctx context.Context, sessionID string, step int, tool string) {
	if a.ckStore == nil || sessionID == "" {
		return
	}
	snap, err := a.ckStore.Get(ctx, sessionID)
	if err != nil || snap == nil {
		return
	}
	if snap.Status != checkpoint.StatusRunning {
		return
	}
	if step > snap.Step {
		snap.Step = step
	}
	if tool != "" {
		if snap.Meta == nil {
			snap.Meta = map[string]any{}
		}
		snap.Meta["lastTool"] = tool
		snap.Meta["lastToolAt"] = time.Now()
	}
	if err := a.ckStore.Save(ctx, snap); err != nil {
		observability.LogError("step checkpoint save", err)
	}
}

// ListResumable returns snapshots of runs that were interrupted by crash/restart:
// Status=running but with no active in-process handle. After a process restart,
// every Status=running snapshot is resumable.
func (a *ChatApp) ListResumable(ctx context.Context) []*checkpoint.Snapshot {
	if a.ckStore == nil {
		return nil
	}
	list, err := a.ckStore.List(ctx, checkpoint.StatusRunning, 200)
	if err != nil {
		return nil
	}
	var out []*checkpoint.Snapshot
	for _, s := range list {
		if s == nil {
			continue
		}
		if a.runs != nil && a.runs.IsRunning(s.SessionID) {
			continue // actively running right now
		}
		out = append(out, s)
	}
	return out
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
		hint += fmt.Sprintf("上次中断于第 %d 步", snap.Step)
	}
	if t, ok := snap.Meta["lastTool"]; ok {
		if hint != "" {
			hint += "，"
		}
		hint += fmt.Sprintf("最后执行工具 %v", t)
	}
	if hint != "" {
		hint += "。"
	}
	if strings.TrimSpace(message) == "" {
		message = "继续。"
	}
	if hint != "" {
		message = message + "\n[断点上下文] " + hint + " 请从断点继续未完成的工作，不要重复已完成的步骤。"
	}
	req := ChatRequest{
		SessionID: sessionID, UserID: snap.UserID, ProjectID: snap.ProjectID,
		Message: message,
	}
	return a.Chat(req)
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

// checkQuota enforces the per-user daily token budget (3.3 observability:
// user-level quota). It reads the running daily usage counter that
// RecordTokenUsage maintains and rejects once the budget is reached.
func (a *ChatApp) checkQuota(ctx context.Context, userID string) error {
	if !a.quotaEnabled || a.redis == nil {
		return nil
	}
	if userID == "" {
		userID = "anonymous"
	}
	used, err := a.redis.Get(ctx, "token:user:"+userID+":"+todayKey())
	if err != nil || used == "" {
		return nil // no record yet → not over
	}
	var usedTok int
	fmt.Sscanf(used, "%d", &usedTok)
	if quotaExceeded(usedTok, a.quotaPerDay) {
		observability.Current().AddQuotaDeny(1)
		return fmt.Errorf("daily token quota (%d) exhausted", a.quotaPerDay)
	}
	return nil
}

// quotaExceeded is a pure predicate so it can be unit-tested without Redis.
func quotaExceeded(used, quota int) bool {
	if quota <= 0 {
		return false
	}
	return used >= quota
}

// todayKey returns the current UTC date (YYYY-MM-DD) for per-day counters.
func todayKey() string {
	return time.Now().UTC().Format("2006-01-02")
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}
