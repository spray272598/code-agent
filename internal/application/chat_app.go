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
	"github.com/spray272598/code-agent/internal/domain/blob"
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
	loop        *engine.Loop
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
	timeoutSec  int
	workspace   string
	rateEnabled bool
	ratePerMin  int
	apiKeys     map[string]bool
}

func NewChatApp(
	loop *engine.Loop,
	sessions sessrepo.ISessionRepository,
	messages sessrepo.IMessageRepository,
	tools *tool.MapRegistry,
	perm *security.Guard,
	redis *redisx.Client,
	timeoutSec int,
	workspace string,
	rateEnabled bool,
	ratePerMin int,
	apiKeys []string,
) *ChatApp {
	km := map[string]bool{}
	for _, k := range apiKeys {
		if k != "" {
			km[k] = true
		}
	}
	if timeoutSec <= 0 {
		timeoutSec = 180
	}
	return &ChatApp{
		loop: loop, sessions: sessions, messages: messages, tools: tools, perm: perm,
		redis: redis, timeoutSec: timeoutSec, workspace: workspace,
		rateEnabled: rateEnabled, ratePerMin: ratePerMin, apiKeys: km,
		slash: slash.NewRegistry(),
	}
}

func (a *ChatApp) SetSkills(s *skill.Service)      { a.skills = s }
func (a *ChatApp) SetMCP(m mcpport.IMCPManagerPort) { a.mcp = m }
func (a *ChatApp) SetMemory(s *memory.Service)     { a.memSvc = s }
func (a *ChatApp) SetAudit(r audit.Repository) { a.auditRepo = r }
func (a *ChatApp) SetBlobStore(s blob.Store)   { a.blobs = s }
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

func (a *ChatApp) Auth(apiKey string) bool {
	if len(a.apiKeys) == 0 {
		return true
	}
	return a.apiKeys[apiKey]
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
		_ = a.redis.Set(ctx, "sess:hot:"+session.ID, req.Message, 24*time.Hour)
	}
	observability.Global.ChatTotal.Add(1)
	res, err := a.loop.Run(ctx, session, req.Message, nil, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact})
	if err != nil && res == nil {
		observability.Global.ChatErrors.Add(1)
		return nil, err
	}
	if res == nil {
		observability.Global.ChatErrors.Add(1)
		return nil, fmt.Errorf("empty result")
	}
	if a.redis != nil && a.redis.Enabled() && res.TokenUsed > 0 {
		day := time.Now().Format("20060102")
		_, _ = a.redis.IncrBy(ctx, fmt.Sprintf("token:user:%s:%s", session.UserID, day), int64(res.TokenUsed), 48*time.Hour)
		_, _ = a.redis.IncrBy(ctx, "token:sess:"+session.ID, int64(res.TokenUsed), 7*24*time.Hour)
	}
	return &ChatResponse{
		SessionID: res.SessionID, Response: res.Response, Steps: res.Steps,
		ToolCalls: res.ToolCalls, TokenUsed: res.TokenUsed,
		NeedPermission: res.NeedPermission, Pending: res.Pending, ErrorClass: res.ErrorClass,
	}, nil
}

func (a *ChatApp) ChatStream(req ChatRequest) (<-chan *engine.Event, *sessmodel.Session, error) {
	forceCompact := false
	if resp, handled, fc := a.trySlash(&req); handled {
		ch := make(chan *engine.Event, 4)
		go func() {
			defer close(ch)
			sid := resp.SessionID
			ch <- &engine.Event{Type: engine.EventSlash, Content: resp.Response, Completed: true, Timestamp: time.Now().UnixMilli()}
			ch <- &engine.Event{Type: engine.EventAnswer, Content: resp.Response, Completed: true, Timestamp: time.Now().UnixMilli()}
			ch <- &engine.Event{Type: engine.EventDone, Content: resp.Response, Completed: true, Timestamp: time.Now().UnixMilli()}
			_ = sid
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.timeoutSec)*time.Second)
	if err := a.checkRate(ctx, req.UserID); err != nil {
		cancel()
		return nil, nil, err
	}
	ch := make(chan *engine.Event, 64)
	go func() {
		defer close(ch)
		defer cancel()
		res, err := a.loop.Run(ctx, session, req.Message, ch, engine.RunOptions{AutoApprove: req.AutoApprove, ForceCompact: forceCompact})
		if err != nil && res == nil {
			ch <- &engine.Event{Type: engine.EventError, Content: err.Error(), Completed: true, Timestamp: time.Now().UnixMilli()}
		}
		if res != nil && a.redis != nil && a.redis.Enabled() && res.TokenUsed > 0 {
			day := time.Now().Format("20060102")
			_, _ = a.redis.IncrBy(ctx, fmt.Sprintf("token:user:%s:%s", session.UserID, day), int64(res.TokenUsed), 48*time.Hour)
		}
	}()
	return ch, session, nil
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
