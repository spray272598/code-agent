package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AgentSpanEvent records agent lifecycle events to the current span.
type AgentSpanEvent struct {
	SpanName    string
	SessionID   string
	StepCount   int
	ToolCalls   int
	TokensUsed  int
	DurationMs  int64
	ErrorClass  string
	Annotations map[string]string
}

// AgentTracer creates and manages spans for agent lifecycle phases.
type AgentTracer struct{}

// StartAgentSpan starts a span for an agent session.
func (AgentTracer) StartAgentSpan(ctx context.Context, sessionID, topology string) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.session",
		attribute.String("session.id", sessionID),
		attribute.String("topology", topology),
	)
	return ctx, span
}

// StartToolSpan starts a span for a tool call within an agent.
func (AgentTracer) StartToolSpan(ctx context.Context, toolName string, sessionID string) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.tool."+toolName,
		attribute.String("tool.name", toolName),
		attribute.String("session.id", sessionID),
	)
	return ctx, span
}

// StartLLMSpan starts a span for an LLM call within an agent.
func (AgentTracer) StartLLMSpan(ctx context.Context, model, sessionID string, inputTokens, outputTokens int) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.llm.call",
		attribute.String("model", model),
		attribute.String("session.id", sessionID),
		attribute.Int("input_tokens", inputTokens),
		attribute.Int("output_tokens", outputTokens),
	)
	return ctx, span
}

// StartCompressionSpan starts a span for context compression.
func (AgentTracer) StartCompressionSpan(ctx context.Context, sessionID string, inputSize, outputSize int) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.context.compress",
		attribute.String("session.id", sessionID),
		attribute.Int("input_chars", inputSize),
		attribute.Int("output_chars", outputSize),
	)
	return ctx, span
}

// StartMemorySpan starts a span for a memory operation.
func (AgentTracer) StartMemorySpan(ctx context.Context, op, sessionID string) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.memory."+op,
		attribute.String("operation", op),
		attribute.String("session.id", sessionID),
	)
	return ctx, span
}

// StartSafetySpan starts a span for a safety check.
func (AgentTracer) StartSafetySpan(ctx context.Context, checkType, sessionID string, denied bool) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.safety."+checkType,
		attribute.String("check_type", checkType),
		attribute.String("session.id", sessionID),
		attribute.Bool("denied", denied),
	)
	return ctx, span
}

// RecordAgentEvent records a structured event to a span.
func (AgentTracer) RecordAgentEvent(span trace.Span, evt AgentSpanEvent) {
	if span == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("session.id", evt.SessionID),
		attribute.Int("step.count", evt.StepCount),
		attribute.Int("tool.calls", evt.ToolCalls),
		attribute.Int("tokens.used", evt.TokensUsed),
		attribute.Int64("duration.ms", evt.DurationMs),
	}
	if evt.ErrorClass != "" {
		attrs = append(attrs, attribute.String("error.class", evt.ErrorClass))
		span.RecordError(fmt.Errorf("%s", evt.ErrorClass))
	}
	for k, v := range evt.Annotations {
		attrs = append(attrs, attribute.String(k, v))
	}
	span.SetAttributes(attrs...)
}

// TraceAgentSession is a convenience wrapper that starts a span, runs fn, and ends the span.
func (AgentTracer) TraceAgentSession(ctx context.Context, sessionID, topology string, fn func(context.Context) error) error {
	ctx, span := StartSpan(ctx, "agent.session",
		attribute.String("session.id", sessionID),
		attribute.String("topology", topology),
	)
	defer span.End()
	start := time.Now()
	err := fn(ctx)
	span.SetAttributes(
		attribute.Int64("duration.ms", time.Since(start).Milliseconds()),
	)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// StartPhaseSpan starts a span for a DeepAgent phase.
func (AgentTracer) StartPhaseSpan(ctx context.Context, sessionID, phaseID, phaseName string) (context.Context, trace.Span) {
	ctx, span := StartSpan(ctx, "agent.phase."+phaseID,
		attribute.String("phase.id", phaseID),
		attribute.String("phase.name", phaseName),
		attribute.String("session.id", sessionID),
	)
	return ctx, span
}

// GlobalAgentTracer is the process-wide agent tracer.
var GlobalAgentTracer = AgentTracer{}

// Compile-time checks.
var (
	_ = GlobalAgentTracer.StartAgentSpan
	_ = GlobalAgentTracer.StartToolSpan
	_ = GlobalAgentTracer.StartLLMSpan
	_ = GlobalAgentTracer.StartCompressionSpan
	_ = GlobalAgentTracer.StartMemorySpan
	_ = GlobalAgentTracer.StartSafetySpan
	_ = GlobalAgentTracer.RecordAgentEvent
	_ = GlobalAgentTracer.TraceAgentSession
	_ = GlobalAgentTracer.StartPhaseSpan
)
