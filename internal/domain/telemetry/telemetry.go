// Package telemetry is the domain-side observability port.
// Domain code depends only on this package — never on infrastructure/observability.
// Bootstrap wires a real Sink (Prometheus/OTLP/logs) via Set.
package telemetry

import (
	"context"
	"time"
)

// Sink is the cross-cutting metrics / trace / log surface for domain code.
type Sink interface {
	IncChatError()
	IncToolCall()
	IncPermissionDeny()
	IncMemoryWrite()
	IncMemoryRead()
	IncBlobOffload()
	IncCompress()
	IncReflect()
	AddTokens(n int64)

	// MCP-specific counters
	IncMCPCacheHit()
	IncMCPCacheMiss()
	IncMCPToolSuccess()
	IncMCPToolError()
	IncCircuitBreakerStateTransition()
	SetCircuitBreakerState(server, state string)

	ObserveLLM(d time.Duration)
	ObserveTool(d time.Duration)

	TraceEvent(fields map[string]any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)

	// StartSpan opens a span; attrs are plain strings (no OTEL types in domain).
	// End must be called (typically via defer end.End()).
	StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanEnd)
	// SpanTool wraps a tool invocation with a short span.
	SpanTool(ctx context.Context, tool string, fn func(context.Context) error) error
}

// SpanEnd closes a span started by StartSpan.
type SpanEnd interface {
	End()
}

// current is the process-wide sink. Defaults to Nop so domain tests need no wiring.
var current Sink = Nop{}

// Set installs the process-wide sink. Pass nil to restore Nop.
func Set(s Sink) {
	if s == nil {
		current = Nop{}
		return
	}
	current = s
}

// Current returns the active sink (never nil).
func Current() Sink {
	if current == nil {
		return Nop{}
	}
	return current
}

// --- convenience wrappers (domain call sites stay short) ---

func IncChatError()              { Current().IncChatError() }
func IncToolCall()               { Current().IncToolCall() }
func IncPermissionDeny()         { Current().IncPermissionDeny() }
func IncMemoryWrite()            { Current().IncMemoryWrite() }
func IncMemoryRead()             { Current().IncMemoryRead() }
func IncBlobOffload()            { Current().IncBlobOffload() }
func IncCompress()               { Current().IncCompress() }
func IncReflect()                { Current().IncReflect() }
func AddTokens(n int64)          { Current().AddTokens(n) }
func IncMCPCacheHit()            { Current().IncMCPCacheHit() }
func IncMCPCacheMiss()           { Current().IncMCPCacheMiss() }
func IncMCPToolSuccess()         { Current().IncMCPToolSuccess() }
func IncMCPToolError()           { Current().IncMCPToolError() }
func IncCircuitBreakerStateTransition() { Current().IncCircuitBreakerStateTransition() }
func SetCircuitBreakerState(server, state string) { Current().SetCircuitBreakerState(server, state) }
func ObserveLLM(d time.Duration) { Current().ObserveLLM(d) }
func ObserveTool(d time.Duration) {
	Current().ObserveTool(d)
}
func TraceEvent(fields map[string]any)                  { Current().TraceEvent(fields) }
func Warnf(format string, args ...any)                  { Current().Warnf(format, args...) }
func Errorf(format string, args ...any)                 { Current().Errorf(format, args...) }
func StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanEnd) {
	return Current().StartSpan(ctx, name, attrs)
}
func SpanTool(ctx context.Context, tool string, fn func(context.Context) error) error {
	return Current().SpanTool(ctx, tool, fn)
}

// Nop is a no-op Sink used by default and in unit tests.
type Nop struct{}

func (Nop) IncChatError()                                {}
func (Nop) IncToolCall()                                 {}
func (Nop) IncPermissionDeny()                           {}
func (Nop) IncMemoryWrite()                              {}
func (Nop) IncMemoryRead()                               {}
func (Nop) IncBlobOffload()                              {}
func (Nop) IncCompress()                                 {}
func (Nop) IncReflect()                                  {}
func (Nop) AddTokens(int64)                              {}
func (Nop) IncMCPCacheHit()                              {}
func (Nop) IncMCPCacheMiss()                             {}
func (Nop) IncMCPToolSuccess()                           {}
func (Nop) IncMCPToolError()                             {}
func (Nop) IncCircuitBreakerStateTransition()            {}
func (Nop) SetCircuitBreakerState(server, state string)  {}
func (Nop) ObserveLLM(time.Duration)                     {}
func (Nop) ObserveTool(time.Duration)                    {}
func (Nop) TraceEvent(map[string]any)                    {}
func (Nop) Warnf(string, ...any)                         {}
func (Nop) Errorf(string, ...any)                        {}
func (Nop) StartSpan(ctx context.Context, _ string, _ map[string]string) (context.Context, SpanEnd) {
	return ctx, nopSpan{}
}
func (Nop) SpanTool(ctx context.Context, _ string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

type nopSpan struct{}

func (nopSpan) End() {}
