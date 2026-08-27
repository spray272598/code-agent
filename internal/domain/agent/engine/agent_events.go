package engine

import (
	"context"
	"fmt"
	"sync"
)

// --- Agent Loop Lifecycle Events ---

// AgentEventType classifies agent loop events for the hook system.
type AgentEventType string

const (
	// AgentPreStep fires before each step. Listeners can reject or replace messages.
	AgentPreStep AgentEventType = "agent/pre-step"
	// AgentRequest fires before LLM call. Listeners can replace model/params.
	AgentRequest AgentEventType = "agent/request"
	// AgentRequestError fires on LLM failure. Listeners can retry or inject fallback.
	AgentRequestError AgentEventType = "agent/request-error"
	// AgentTurnStopping fires when a turn is about to close. Listeners may steer.
	AgentTurnStopping AgentEventType = "agent/turn-stopping"

	// ToolPreExecute fires before tool execution. Listeners can allow/deny/ask.
	ToolPreExecute AgentEventType = "tools/pre-execute"
	// ToolExecute wraps tool execution (around-middleware for timeout/retry).
	ToolExecute AgentEventType = "tools/execute"
	// ToolPostExecute fires after tool execution. Listeners can accept/replace/block.
	ToolPostExecute AgentEventType = "tools/post-execute"
	// ToolResult is a notification of the final frozen outcome.
	ToolResult AgentEventType = "tools/result"
)

// --- Event Payloads ---

// PreStepEvent is the payload for agent/pre-step.
type PreStepEvent struct {
	SessionID string
	Step      int
	Messages  []any // mutable; listeners can modify
}

// RequestEvent is the payload for agent/request.
type RequestEvent struct {
	SessionID string
	Model     string
	Messages  int
	// Mutable fields listeners can override:
	ModelOverride string
	Temperature   *float64
	MaxTokens     *int
}

// RequestErrorEvent is the payload for agent/request-error.
type RequestErrorEvent struct {
	SessionID string
	Step      int
	Error     error
	Retried   bool // set to true if listener handles retry
}

// ToolEvent is the payload for tool execution events.
type ToolEvent struct {
	SessionID string
	Step      int
	ToolName  string
	Args      map[string]any
	Result    string
	Error     error
	Deny      bool   // set by pre-execute listeners to deny
	DenyMsg   string // deny reason
	Replace   string // set by post-execute listeners to replace result
	Block     bool   // set by post-execute listeners to block result
}

// --- Waterfall Middleware ---

// WaterfallHandler is an around-middleware for agent events.
// Call next(ctx) to continue the chain; returning early stops execution.
type WaterfallHandler func(ctx context.Context, payload any, next func(ctx context.Context) error) error

// SerialHandler is a fire-and-forget listener.
type SerialHandler func(ctx context.Context, payload any) error

// --- AgentEventBus ---

// AgentEventBus provides waterfall (around-middleware) and serial (fire-and-forget)
// dispatch for agent loop lifecycle events.
type AgentEventBus struct {
	mu         sync.RWMutex
	waterfalls map[AgentEventType][]WaterfallHandler
	serials    map[AgentEventType][]SerialHandler
}

// NewAgentEventBus creates a new event bus.
func NewAgentEventBus() *AgentEventBus {
	return &AgentEventBus{
		waterfalls: make(map[AgentEventType][]WaterfallHandler),
		serials:    make(map[AgentEventType][]SerialHandler),
	}
}

// OnWaterfall registers an around-middleware handler for an event type.
func (b *AgentEventBus) OnWaterfall(typ AgentEventType, h WaterfallHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.waterfalls[typ] = append(b.waterfalls[typ], h)
}

// On registers a serial (fire-and-forget) handler for an event type.
func (b *AgentEventBus) On(typ AgentEventType, h SerialHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.serials[typ] = append(b.serials[typ], h)
}

// Emit runs serial handlers. Errors are logged but do not stop execution.
func (b *AgentEventBus) Emit(ctx context.Context, typ AgentEventType, payload any) {
	b.mu.RLock()
	hs := append([]SerialHandler{}, b.serials[typ]...)
	b.mu.RUnlock()
	for _, h := range hs {
		if err := h(ctx, payload); err != nil {
			fmt.Printf("[agent-event] %s error: %v\n", typ, err)
		}
	}
}

// Waterfall runs waterfall handlers around an inner function.
// Each handler can call next(ctx) to continue, or return early to short-circuit.
// If no handlers are registered, next(ctx) is called directly.
func (b *AgentEventBus) Waterfall(ctx context.Context, typ AgentEventType, payload any, next func(ctx context.Context) error) error {
	b.mu.RLock()
	hs := append([]WaterfallHandler{}, b.waterfalls[typ]...)
	b.mu.RUnlock()

	if len(hs) == 0 {
		return next(ctx)
	}

	// Build the chain from inside out
	chain := next
	for i := len(hs) - 1; i >= 0; i-- {
		h := hs[i]
		nextFn := chain
		chain = func(ctx context.Context) error {
			return h(ctx, payload, nextFn)
		}
	}
	return chain(ctx)
}

// HasListeners returns true if any handlers are registered for the given event type.
func (b *AgentEventBus) HasListeners(typ AgentEventType) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.waterfalls[typ]) > 0 || len(b.serials[typ]) > 0
}
