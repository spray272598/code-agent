package tool

import "context"

// ITool is a single callable tool (core six + MCP adapters).
type ITool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, args map[string]any) (Result, error)
}

// Result of a tool invocation.
type Result struct {
	Text     string
	// ObjectKey if large payload was offloaded to object storage.
	ObjectKey string
	IsError   bool
}

// Registry holds tools for the agent loop.
type Registry interface {
	Register(t ITool)
	Unregister(name string)
	Get(name string) ITool
	List() []ITool
}

// --- Session context ---

type ctxKey int

const (
	ctxSessionID ctxKey = iota + 1
)

// WithSessionID injects sessionID into context for tools.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxSessionID, sessionID)
}

// SessionIDFrom extracts sessionID from context.
func SessionIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxSessionID).(string)
	return s
}
