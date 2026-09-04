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

// WithMCP injects a single MCP Manager (single-operator harness).
func WithMCP(m mcpport.IMCPManagerPort) Option {
	return func(a *ChatApp) { a.mcpFactory = mcpport.NewSingleManagerFactory(m) }
}

// WithMCPFactory injects the MCP manager factory. The harness is single-operator,
// so this is normally NewSingleManagerFactory; tests may inject a stub.
func WithMCPFactory(f mcpport.IMCPManagerFactory) Option {
	return func(a *ChatApp) { a.mcpFactory = f }
}

// WithMemory injects memory service.
func WithMemory(s *memory.Service) Option {
	return func(a *ChatApp) { a.memSvc = s }
}

// WithSummaryRepo injects the rolling session-summary repository, enabling
// long-task cross-segment memory solidification (RunBackground checkpoints).
func WithSummaryRepo(r sessrepo.ISummaryRepository) Option {
	return func(a *ChatApp) { a.summaryRepo = r }
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
//
// It must mutate the service created by New rather than replace it: the
// constructor seeds timeoutSec/workspace/redis from CoreDeps, and those fields
// drive the per-run context deadline. Wholesale replacement silently zeroed the
// timeout, which made every request fail immediately with
// "context deadline exceeded".
func WithCheckpoint(store checkpoint.Store, runs *checkpoint.RunRegistry) Option {
	return func(a *ChatApp) {
		if runs == nil {
			runs = checkpoint.NewRunRegistry()
		}
		if a.cp == nil {
			a.cp = &CheckpointService{perm: a.perm}
		}
		a.cp.ckStore = store
		a.cp.runs = runs
		a.cp.perm = a.perm
	}
}

// WithSSH injects SSH pool and connection repository.
func WithSSH(pool *sshinfra.Pool, repo sshport.IConnectionRepository) Option {
	return func(a *ChatApp) {
		a.sshSvc = &SSHService{pool: pool, repo: repo}
	}
}

// CoreDeps required dependencies for ChatApp construction.
type CoreDeps struct {
	// Loop is the agent orchestrator (native *engine.Loop or Eino runner).
	Loop         engine.Runner
	Sessions     sessrepo.ISessionRepository
	Messages     sessrepo.IMessageRepository
	Tools        *tool.MapRegistry
	Perm         *security.Guard
	Redis        *redisx.Client
	TimeoutSec   int
	Workspace    string
	RateEnabled  bool
	RatePerMin   int
	APIKeys      []string // hashed at construct; prefer WithKeyStore for pre-hashed
	QuotaEnabled bool     // per-user daily token quota (3.3)
	QuotaPerDay  int      // tokens allowed per user per day (0 = unlimited)
}

// New builds ChatApp from required deps + optional Option list.
func New(core CoreDeps, opts ...Option) *ChatApp {
	if core.TimeoutSec <= 0 {
		core.TimeoutSec = 180
	}
	a := &ChatApp{
		loop: core.Loop, sessions: core.Sessions, messages: core.Messages,
		tools: core.Tools, perm: core.Perm, redis: core.Redis,
		workspace: core.Workspace,
		keys:  auth.NewKeyStore(core.APIKeys),
		slash: slash.NewRegistry(),
		rateSvc: &RateQuotaService{
			redis:        core.Redis,
			rateEnabled:  core.RateEnabled,
			ratePerMin:   core.RatePerMin,
			quotaEnabled: core.QuotaEnabled && core.QuotaPerDay > 0,
			quotaPerDay:  core.QuotaPerDay,
		},
		cp: &CheckpointService{
			ckStore: nil, perm: core.Perm, redis: core.Redis,
			timeoutSec: core.TimeoutSec, workspace: core.Workspace,
		},
		idemSvc: &IdempotencyService{},
	}
	for _, o := range opts {
		if o != nil {
			o(a)
		}
	}
	// Wire perm into CheckpointService after options are applied
	if a.cp != nil {
		a.cp.perm = a.perm
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

// WithTokenQuota enables the per-user daily token budget (3.3 observability:
// user-level quota). perDay <= 0 disables regardless of enabled.
func WithTokenQuota(enabled bool, perDay int) Option {
	return func(a *ChatApp) {
		if a.rateSvc != nil {
			a.rateSvc.quotaEnabled = enabled && perDay > 0
			a.rateSvc.quotaPerDay = perDay
		}
	}
}
