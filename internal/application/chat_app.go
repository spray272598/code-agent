package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
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
	}
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
}

func (a *ChatApp) Chat(req ChatRequest) (*ChatResponse, error) {
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
	res, err := a.loop.Run(ctx, session, req.Message, nil, engine.RunOptions{AutoApprove: req.AutoApprove})
	if err != nil && res == nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("empty result")
	}
	// token meter
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
		res, err := a.loop.Run(ctx, session, req.Message, ch, engine.RunOptions{AutoApprove: req.AutoApprove})
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
