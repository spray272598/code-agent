package observability

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// OTelMetrics implements CounterRegistry using OpenTelemetry Metrics API.
// This replaces the atomic.Int64-based Metrics struct with proper OTel instruments.
type OTelMetrics struct {
	meter metric.Meter

	// Counters
	chatTotal      metric.Int64Counter
	chatErrors     metric.Int64Counter
	toolCalls      metric.Int64Counter
	permissionDeny metric.Int64Counter
	memoryWrites   metric.Int64Counter
	memoryReads    metric.Int64Counter
	tokensTotal    metric.Int64Counter
	reflectTotal   metric.Int64Counter
	compressTotal  metric.Int64Counter
	blobOffload    metric.Int64Counter
	embeddingCalls metric.Int64Counter
	embeddingErrs  metric.Int64Counter
	quotaDeny      metric.Int64Counter

	// MCP counters
	mcpCacheHits   metric.Int64Counter
	mcpCacheMisses metric.Int64Counter
	mcpToolSuccess metric.Int64Counter
	mcpToolErrors  metric.Int64Counter
	cbTransitions  metric.Int64Counter

	// Histograms for latency
	llmLatency  metric.Int64Histogram
	toolLatency metric.Int64Histogram

	// Gauge for circuit breaker state (per server)
	cbState metric.Int64ObservableGauge

	// cbStates tracks per-server circuit breaker state for the gauge callback.
	cbStatesMu sync.RWMutex
	cbStates   map[string]string
}

// NewOTelMetrics creates an OTel-backed metrics registry.
// meterName is the meter scope name (e.g. "code-agent").
func NewOTelMetrics(meterName string) (*OTelMetrics, error) {
	meter := otel.Meter(meterName,
		metric.WithInstrumentationVersion("0.6.0"),
		metric.WithSchemaURL(semconv.SchemaURL),
	)

	m := &OTelMetrics{
		meter:    meter,
		cbStates: make(map[string]string),
	}

	var err error

	// Counters
	if m.chatTotal, err = meter.Int64Counter("code_agent_chat_total",
		metric.WithDescription("Total chat requests")); err != nil {
		return nil, err
	}
	if m.chatErrors, err = meter.Int64Counter("code_agent_chat_errors_total",
		metric.WithDescription("Chat errors")); err != nil {
		return nil, err
	}
	if m.toolCalls, err = meter.Int64Counter("code_agent_tool_calls_total",
		metric.WithDescription("Tool invocations")); err != nil {
		return nil, err
	}
	if m.permissionDeny, err = meter.Int64Counter("code_agent_permission_deny_total",
		metric.WithDescription("Permission denials")); err != nil {
		return nil, err
	}
	if m.memoryWrites, err = meter.Int64Counter("code_agent_memory_writes_total",
		metric.WithDescription("Memory writes")); err != nil {
		return nil, err
	}
	if m.memoryReads, err = meter.Int64Counter("code_agent_memory_reads_total",
		metric.WithDescription("Memory reads")); err != nil {
		return nil, err
	}
	if m.tokensTotal, err = meter.Int64Counter("code_agent_tokens_total",
		metric.WithDescription("Tokens accounted")); err != nil {
		return nil, err
	}
	if m.reflectTotal, err = meter.Int64Counter("code_agent_reflect_total",
		metric.WithDescription("Reflect invocations")); err != nil {
		return nil, err
	}
	if m.compressTotal, err = meter.Int64Counter("code_agent_compress_total",
		metric.WithDescription("Context compressions")); err != nil {
		return nil, err
	}
	if m.blobOffload, err = meter.Int64Counter("code_agent_blob_offload_total",
		metric.WithDescription("Large tool results offloaded")); err != nil {
		return nil, err
	}
	if m.embeddingCalls, err = meter.Int64Counter("code_agent_embedding_calls_total",
		metric.WithDescription("Embedding API calls")); err != nil {
		return nil, err
	}
	if m.embeddingErrs, err = meter.Int64Counter("code_agent_embedding_errors_total",
		metric.WithDescription("Embedding API errors")); err != nil {
		return nil, err
	}
	if m.quotaDeny, err = meter.Int64Counter("code_agent_quota_deny_total",
		metric.WithDescription("Token-quota denials")); err != nil {
		return nil, err
	}

	// MCP counters
	if m.mcpCacheHits, err = meter.Int64Counter("code_agent_mcp_cache_hits_total",
		metric.WithDescription("MCP tool cache hits")); err != nil {
		return nil, err
	}
	if m.mcpCacheMisses, err = meter.Int64Counter("code_agent_mcp_cache_misses_total",
		metric.WithDescription("MCP tool cache misses")); err != nil {
		return nil, err
	}
	if m.mcpToolSuccess, err = meter.Int64Counter("code_agent_mcp_tool_success_total",
		metric.WithDescription("MCP tool successful calls")); err != nil {
		return nil, err
	}
	if m.mcpToolErrors, err = meter.Int64Counter("code_agent_mcp_tool_errors_total",
		metric.WithDescription("MCP tool failed calls")); err != nil {
		return nil, err
	}
	if m.cbTransitions, err = meter.Int64Counter("code_agent_circuit_breaker_transitions_total",
		metric.WithDescription("Circuit breaker state transitions")); err != nil {
		return nil, err
	}

	// Histograms
	if m.llmLatency, err = meter.Int64Histogram("code_agent_llm_latency_ms",
		metric.WithDescription("LLM call latency in milliseconds"),
		metric.WithUnit("ms")); err != nil {
		return nil, err
	}
	if m.toolLatency, err = meter.Int64Histogram("code_agent_tool_latency_ms",
		metric.WithDescription("Tool call latency in milliseconds"),
		metric.WithUnit("ms")); err != nil {
		return nil, err
	}

	// Circuit breaker state gauge (callback-based)
	if m.cbState, err = meter.Int64ObservableGauge("code_agent_circuit_breaker_state",
		metric.WithDescription("Current circuit breaker state per MCP server"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			m.cbStatesMu.RLock()
			defer m.cbStatesMu.RUnlock()
			for server, state := range m.cbStates {
				var val int64
				switch state {
				case "half_open":
					val = 1
				case "open":
					val = 2
				default:
					val = 0
				}
				o.Observe(val, metric.WithAttributes(
					attribute.String("server", server),
					attribute.String("state", state),
				))
			}
			return nil
		})); err != nil {
		return nil, err
	}

	return m, nil
}

// --- CounterRegistry interface implementation ---

func (m *OTelMetrics) AddChatTotal(n int64)                 { m.chatTotal.Add(context.Background(), n) }
func (m *OTelMetrics) AddChatErrors(n int64)                { m.chatErrors.Add(context.Background(), n) }
func (m *OTelMetrics) AddToolCalls(n int64)                 { m.toolCalls.Add(context.Background(), n) }
func (m *OTelMetrics) AddPermissionDeny(n int64)            { m.permissionDeny.Add(context.Background(), n) }
func (m *OTelMetrics) AddMemoryWrites(n int64)              { m.memoryWrites.Add(context.Background(), n) }
func (m *OTelMetrics) AddMemoryReads(n int64)               { m.memoryReads.Add(context.Background(), n) }
func (m *OTelMetrics) AddTokens(n int64)                    { m.tokensTotal.Add(context.Background(), n) }
func (m *OTelMetrics) AddReflect(n int64)                   { m.reflectTotal.Add(context.Background(), n) }
func (m *OTelMetrics) AddCompress(n int64)                  { m.compressTotal.Add(context.Background(), n) }
func (m *OTelMetrics) AddBlobOffload(n int64)               { m.blobOffload.Add(context.Background(), n) }
func (m *OTelMetrics) AddEmbeddingCalls(n int64)            { m.embeddingCalls.Add(context.Background(), n) }
func (m *OTelMetrics) AddEmbeddingErrors(n int64)           { m.embeddingErrs.Add(context.Background(), n) }
func (m *OTelMetrics) AddQuotaDeny(n int64)                 { m.quotaDeny.Add(context.Background(), n) }
func (m *OTelMetrics) AddMCPCacheHits(n int64)              { m.mcpCacheHits.Add(context.Background(), n) }
func (m *OTelMetrics) AddMCPCacheMisses(n int64)            { m.mcpCacheMisses.Add(context.Background(), n) }
func (m *OTelMetrics) AddMCPToolSuccess(n int64)            { m.mcpToolSuccess.Add(context.Background(), n) }
func (m *OTelMetrics) AddMCPToolErrors(n int64)             { m.mcpToolErrors.Add(context.Background(), n) }
func (m *OTelMetrics) AddCircuitBreakerTransitions(n int64) { m.cbTransitions.Add(context.Background(), n) }

func (m *OTelMetrics) SetCircuitBreakerState(server, state string) {
	m.cbStatesMu.Lock()
	m.cbStates[server] = state
	m.cbStatesMu.Unlock()
}

func (m *OTelMetrics) ObserveLLM(d time.Duration) {
	m.llmLatency.Record(context.Background(), d.Milliseconds())
}

func (m *OTelMetrics) ObserveTool(d time.Duration) {
	m.toolLatency.Record(context.Background(), d.Milliseconds())
}

// Snapshot returns a map for backward-compatible Prometheus exposition.
func (m *OTelMetrics) Snapshot() map[string]any {
	// OTel metrics are exported via OTLP; snapshot is for legacy endpoints.
	return map[string]any{
		"backend": "otel",
	}
}

// SetupOTelMetrics initializes OTel metrics and sets it as the active CounterRegistry.
// Returns a shutdown function.
func SetupOTelMetrics(ctx context.Context, meterName string) (func(context.Context) error, error) {
	m, err := NewOTelMetrics(meterName)
	if err != nil {
		return nil, err
	}
	SetMetrics(m)
	return func(ctx context.Context) error { return nil }, nil
}
