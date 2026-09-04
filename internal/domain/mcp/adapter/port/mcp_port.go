package port

import (
	"context"
	"errors"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// IMCPClient is a single MCP server connection (implemented in infrastructure).
type IMCPClient interface {
	Name() string
	Initialize(ctx context.Context) error
	// Ping sends an MCP-level ping (JSON-RPC "ping") to keep the session alive
	// and detect a dead peer. Must return nil only if the server answered.
	Ping(ctx context.Context) error
	ListTools(ctx context.Context) ([]model.ToolDef, error)
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	ListResources(ctx context.Context) ([]model.ResourceDef, error)
	ReadResource(ctx context.Context, uri string) (*model.ResourceContent, error)
	ListPrompts(ctx context.Context) ([]model.PromptDef, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) ([]model.PromptMessage, error)
	Close() error
}

// IMCPManagerPort is the domain-facing MCP lifecycle API.
// Domain services depend on this interface only — never on infrastructure/mcp.
//
// Sprint 1.6: callers MUST obtain a per-user Manager via IUserMCPManagerFactory
// (For(ctx)) rather than holding a long-lived Manager pointer.
type IMCPManagerPort interface {
	AddOrUpdate(ctx context.Context, cfg model.ServerConfig) error
	Remove(name string) error
	ListTools(ctx context.Context) ([]model.ToolDef, error)
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	ListResources(ctx context.Context) ([]model.ResourceDef, error)
	ReadResource(ctx context.Context, uri string) (*model.ResourceContent, error)
	ListPrompts(ctx context.Context) ([]model.PromptDef, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) ([]model.PromptMessage, error)
	Health(ctx context.Context) []model.HealthStatus
	IsOnline(name string) bool
}

// FactoryMetric exposes the per-user factory counters (created/reused/returns)
// so observability can verify the factory is actually in use.
type FactoryMetric struct {
	Created int64
	Reused  int64
	Returns int64
}

// IMCPManagerFactory resolves the (single, operator-scoped) MCP Manager for a
// request context. The harness has no accounts or tenants, so there is exactly
// one Manager; the factory exists only to keep the call site uniform with the
// domain layer and to allow tests to inject a stub.
type IMCPManagerFactory interface {
	For(ctx context.Context) (IMCPManagerPort, error)
}

// NewSingleManagerFactory wraps one Manager into an IMCPManagerFactory. Used by
// bootstrap (the system Manager) and by tests. It is the post-tenant replacement
// for the old per-user UserFactory, which minted one Manager per account.
func NewSingleManagerFactory(mgr IMCPManagerPort) IMCPManagerFactory {
	return &singleManagerFactory{inner: mgr}
}

type singleManagerFactory struct{ inner IMCPManagerPort }

func (f *singleManagerFactory) For(_ context.Context) (IMCPManagerPort, error) {
	if f.inner == nil {
		return nil, ErrNoMCPManager
	}
	return f.inner, nil
}

// ErrNoMCPManager is returned when no Manager was wired.
var ErrNoMCPManager = errors.New("mcp manager not configured")
