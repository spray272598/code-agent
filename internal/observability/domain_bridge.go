package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/spray272598/code-agent/internal/domain/telemetry"
)

// DomainBridge adapts package-level metrics/OTLP/log helpers to domain/telemetry.Sink.
// Wire once in bootstrap: telemetry.Set(observability.DomainBridge{}).
type DomainBridge struct{}

var _ telemetry.Sink = DomainBridge{}

func (DomainBridge) IncChatError()      { Current().AddChatErrors(1) }
func (DomainBridge) IncToolCall()       { Current().AddToolCalls(1) }
func (DomainBridge) IncPermissionDeny() { Current().AddPermissionDeny(1) }
func (DomainBridge) IncMemoryWrite()    { Current().AddMemoryWrites(1) }
func (DomainBridge) IncMemoryRead()     { Current().AddMemoryReads(1) }
func (DomainBridge) IncBlobOffload()    { Current().AddBlobOffload(1) }
func (DomainBridge) IncCompress()       { Current().AddCompress(1) }
func (DomainBridge) IncReflect()        { Current().AddReflect(1) }
func (DomainBridge) AddTokens(n int64)  { Current().AddTokens(n) }
func (DomainBridge) IncMCPCacheHit()    { Current().AddMCPCacheHits(1) }
func (DomainBridge) IncMCPCacheMiss()   { Current().AddMCPCacheMisses(1) }
func (DomainBridge) IncMCPToolSuccess() { Current().AddMCPToolSuccess(1) }
func (DomainBridge) IncMCPToolError()   { Current().AddMCPToolErrors(1) }
func (DomainBridge) IncCircuitBreakerStateTransition() { Current().AddCircuitBreakerTransitions(1) }
func (DomainBridge) SetCircuitBreakerState(server, state string) {
	Current().SetCircuitBreakerState(server, state)
}
func (DomainBridge) ObserveLLM(d time.Duration) {
	Current().ObserveLLM(d)
}
func (DomainBridge) ObserveTool(d time.Duration) {
	Current().ObserveTool(d)
}
func (DomainBridge) TraceEvent(fields map[string]any) { Trace.Event(fields) }
func (DomainBridge) Warnf(format string, args ...any) { Warnf(format, args...) }
func (DomainBridge) Errorf(format string, args ...any) {
	Errorf(format, args...)
}

func (DomainBridge) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, telemetry.SpanEnd) {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		kvs = append(kvs, attribute.String(k, v))
	}
	ctx, span := StartSpan(ctx, name, kvs...)
	return ctx, otelSpan{span: span}
}

func (DomainBridge) SpanTool(ctx context.Context, tool string, fn func(context.Context) error) error {
	return SpanTool(ctx, tool, fn)
}

type otelSpan struct {
	span trace.Span
}

func (s otelSpan) End() {
	if s.span != nil {
		s.span.End()
	}
}
