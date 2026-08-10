package application

import (
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/slash"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
)

// Option configures optional ChatApp dependencies (functional options).
type Option func(*ChatApp)

// WithSkills injects skill service.
func WithSkills(s *skill.Service) Option {
	return func(a *ChatApp) { a.skills = s }
}

// WithMCP injects MCP manager.
func WithMCP(m mcpport.IMCPManagerPort) Option {
	return func(a *ChatApp) { a.mcp = m }
}

// WithMemory injects memory service.
func WithMemory(s *memory.Service) Option {
	return func(a *ChatApp) { a.memSvc = s }
}

// WithAudit injects audit repository.
func WithAudit(r audit.Repository) Option {
	return func(a *ChatApp) { a.auditRepo = r }
}

// WithBlobStore injects blob store.
func WithBlobStore(s blob.Store) Option {
	return func(a *ChatApp) { a.blobs = s }
}

// WithSlashRegistry replaces default slash registry.
func WithSlashRegistry(r *slash.Registry) Option {
	return func(a *ChatApp) {
		if r != nil {
			a.slash = r
		}
	}
}

// WithKeyStore injects a pre-built key store (keys already hashed).
func WithKeyStore(k *auth.KeyStore) Option {
	return func(a *ChatApp) {
		if k != nil {
			a.keys = k
		}
	}
}

// WithCheckpoint injects durable interrupt store + run registry.
func WithCheckpoint(store checkpoint.Store, runs *checkpoint.RunRegistry) Option {
	return func(a *ChatApp) {
		a.ckStore = store
		a.runs = runs
		if a.runs == nil {
			a.runs = checkpoint.NewRunRegistry()
		}
	}
}

// WithSSH injects SSH pool and connection repository.
func WithSSH(pool *sshinfra.Pool, repo sshport.IConnectionRepository) Option {
	return func(a *ChatApp) {
		a.sshPool = pool
		a.sshRepo = repo
	}
}

// CoreDeps required dependencies for ChatApp construction.
type CoreDeps struct {
	// Loop is the agent orchestrator (native *engine.Loop or Eino runner).
	Loop        engine.Runner
	Sessions    sessrepo.ISessionRepository
	Messages    sessrepo.IMessageRepository
	Tools       *tool.MapRegistry
	Perm        *security.Guard
	Redis       *redisx.Client
	TimeoutSec  int
	Workspace   string
	RateEnabled bool
	RatePerMin  int
	APIKeys     []string // hashed at construct; prefer WithKeyStore for pre-hashed
}

// New builds ChatApp from required deps + optional Option list.
func New(core CoreDeps, opts ...Option) *ChatApp {
	if core.TimeoutSec <= 0 {
		core.TimeoutSec = 180
	}
	a := &ChatApp{
		loop: core.Loop, sessions: core.Sessions, messages: core.Messages,
		tools: core.Tools, perm: core.Perm, redis: core.Redis,
		timeoutSec: core.TimeoutSec, workspace: core.Workspace,
		rateEnabled: core.RateEnabled, ratePerMin: core.RatePerMin,
		keys:  auth.NewKeyStore(core.APIKeys),
		slash: slash.NewRegistry(),
	}
	for _, o := range opts {
		if o != nil {
			o(a)
		}
	}
	return a
}

// NewChatApp keeps backward-compatible constructor; prefer New + Option.
func NewChatApp(
	loop engine.Runner,
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
	return New(CoreDeps{
		Loop: loop, Sessions: sessions, Messages: messages, Tools: tools,
		Perm: perm, Redis: redis, TimeoutSec: timeoutSec, Workspace: workspace,
		RateEnabled: rateEnabled, RatePerMin: ratePerMin, APIKeys: apiKeys,
	})
}
