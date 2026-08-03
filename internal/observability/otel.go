package observability

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer is the package tracer for code-agent.
var Tracer = otel.Tracer("code-agent")

// OTLPConfig for optional export.
type OTLPConfig struct {
	Enabled  bool
	Endpoint string // e.g. localhost:4318 (HTTP)
	Insecure bool
	Service  string
}

// SetupTracer configures global TracerProvider. Returns shutdown func.
func SetupTracer(ctx context.Context, cfg OTLPConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "localhost:4318"
	}
	service := cfg.Service
	if service == "" {
		service = "code-agent"
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(service),
			attribute.String("service.version", "0.6.0"),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	Tracer = otel.Tracer("code-agent")
	log.Printf("[otel] OTLP HTTP exporter -> %s service=%s\n", endpoint, service)

	return tp.Shutdown, nil
}

// StartSpan helper.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// SpanFromTool records a short tool span.
func SpanTool(ctx context.Context, tool string, fn func(context.Context) error) error {
	ctx, span := StartSpan(ctx, "tool."+tool, attribute.String("tool.name", tool))
	defer span.End()
	t0 := time.Now()
	err := fn(ctx)
	span.SetAttributes(attribute.Int64("duration_ms", time.Since(t0).Milliseconds()))
	if err != nil {
		span.RecordError(err)
	}
	return err
}
