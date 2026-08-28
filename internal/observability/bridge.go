package observability

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// EventType identifies the kind of observability event.
type EventType string

const (
	EventSessionStart   EventType = "session_start"
	EventSessionEnd     EventType = "session_end"
	EventSessionError   EventType = "session_error"
	EventToolCall       EventType = "tool_call"
	EventToolResult     EventType = "tool_result"
	EventToolError      EventType = "tool_error"
	EventLLMCall        EventType = "llm_call"
	EventLLMResult      EventType = "llm_result"
	EventCompression    EventType = "compression"
	EventMemoryOp       EventType = "memory_op"
	EventPermission     EventType = "permission"
	EventSafetyCheck    EventType = "safety_check"
	EventTopologySelect EventType = "topology_select"
)

// ObservabilityEvent is a structured event for observability pipelines.
type ObservabilityEvent struct {
	Type       EventType      `json:"type"`
	Timestamp  time.Time      `json:"ts"`
	SessionID  string         `json:"sessionId,omitempty"`
	UserID     string         `json:"userId,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	DurationMs int64          `json:"durationMs,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// ObservabilityBridge routes observability events to registered handlers.
type ObservabilityBridge struct {
	mu       sync.RWMutex
	handlers []EventHandler
	enabled  bool
}

// EventHandler processes observability events.
type EventHandler interface {
	HandleEvent(ctx context.Context, evt ObservabilityEvent)
}

// NewObservabilityBridge creates an event bridge with no handlers.
func NewObservabilityBridge() *ObservabilityBridge {
	return &ObservabilityBridge{
		handlers: make([]EventHandler, 0),
		enabled:  true,
	}
}

// RegisterHandler adds an event handler.
func (b *ObservabilityBridge) RegisterHandler(h EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// SetEnabled controls whether events are dispatched.
func (b *ObservabilityBridge) SetEnabled(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.enabled = v
}

// Emit dispatches an event to all registered handlers.
func (b *ObservabilityBridge) Emit(ctx context.Context, evt ObservabilityEvent) {
	b.mu.RLock()
	if !b.enabled || len(b.handlers) == 0 {
		b.mu.RUnlock()
		return
	}
	handlers := make([]EventHandler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	evt.Timestamp = time.Now()
	for _, h := range handlers {
		func(handler EventHandler) {
			defer func() {
				if r := recover(); r != nil {
					slog.Default().Error("observability handler panic", "error", r)
				}
			}()
			handler.HandleEvent(ctx, evt)
		}(h)
	}
}

// --- MetricEventHandler: bridges events to the CounterRegistry ---

// MetricEventHandler translates observability events to metrics counters.
type MetricEventHandler struct{}

// HandleEvent updates metrics based on event type.
func (MetricEventHandler) HandleEvent(_ context.Context, evt ObservabilityEvent) {
	m := Current()
	switch evt.Type {
	case EventSessionStart:
		m.AddChatTotal(1)
	case EventSessionError:
		m.AddChatErrors(1)
	case EventToolCall:
		m.AddToolCalls(1)
	case EventPermission:
		m.AddPermissionDeny(1)
	case EventMemoryOp:
		if op, ok := evt.Data["op"].(string); ok {
			if op == "write" {
				m.AddMemoryWrites(1)
			} else {
				m.AddMemoryReads(1)
			}
		}
	case EventCompression:
		m.AddCompress(1)
	case EventToolError:
		m.AddChatErrors(1)
	}
}

// --- LogEventHandler: logs events for debugging ---

// LogEventHandler logs observability events at debug level.
type LogEventHandler struct {
	Verbose bool
}

// HandleEvent logs the event.
func (h LogEventHandler) HandleEvent(_ context.Context, evt ObservabilityEvent) {
	if h.Verbose {
		slog.Default().Debug("observability event",
			"type", evt.Type,
			"session_id", evt.SessionID,
			"duration_ms", evt.DurationMs,
			"data", evt.Data,
			"error", evt.Error,
		)
	}
}

// GlobalObservabilityBridge is the process-wide observability bridge.
var GlobalObservabilityBridge = NewObservabilityBridge()

// Compile-time checks.
var (
	_ = GlobalObservabilityBridge.Emit
	_ = MetricEventHandler{}.HandleEvent
	_ = LogEventHandler{}.HandleEvent
)
