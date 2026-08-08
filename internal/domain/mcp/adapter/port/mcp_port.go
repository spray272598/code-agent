package port

import (
	"context"

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
	Close() error
}

// IMCPManagerPort is the domain-facing MCP lifecycle API.
// Domain services depend on this interface only — never on infrastructure/mcp.
type IMCPManagerPort interface {
	AddOrUpdate(ctx context.Context, cfg model.ServerConfig) error
	Remove(name string) error
	ListTools(ctx context.Context) ([]model.ToolDef, error)
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	Health(ctx context.Context) []model.HealthStatus
	IsOnline(name string) bool
}
