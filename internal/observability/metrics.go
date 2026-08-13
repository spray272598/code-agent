package observability

import (
	"sync/atomic"
	"time"
)

// CounterRegistry is the injectable metrics surface used by infrastructure / application.
// Prefer Current() over the legacy Global variable so tests can SetMetrics(fake).
type CounterRegistry interface {
	AddChatTotal(n int64)
	AddChatErrors(n int64)
	AddToolCalls(n int64)
	AddPermissionDeny(n int64)
	AddMemoryWrites(n int64)
	AddMemoryReads(n int64)
	AddTokens(n int64)
	AddReflect(n int64)
	AddCompress(n int64)
	AddBlobOffload(n int64)
	AddEmbeddingCalls(n int64)
	AddEmbeddingErrors(n int64)
	ObserveLLM(d time.Duration)
	ObserveTool(d time.Duration)
	Snapshot() map[string]any
}

// Metrics is the default process-local CounterRegistry implementation.
type Metrics struct {
	ChatTotal      atomic.Int64
	ChatErrors     atomic.Int64
	ToolCalls      atomic.Int64
	PermissionDeny atomic.Int64
	MemoryWrites   atomic.Int64
	MemoryReads    atomic.Int64
	TokensTotal    atomic.Int64
	ReflectTotal   atomic.Int64
	CompressTotal  atomic.Int64
	BlobOffload    atomic.Int64
	EmbeddingCalls atomic.Int64
	EmbeddingErrs  atomic.Int64

	LLMLatencySumMs  atomic.Int64
	LLMLatencyCount  atomic.Int64
	ToolLatencySumMs atomic.Int64
	ToolLatencyCount atomic.Int64
}

// NewMetrics returns an empty metrics registry (useful in tests).
func NewMetrics() *Metrics { return &Metrics{} }

func (m *Metrics) AddChatTotal(n int64)      { m.ChatTotal.Add(n) }
func (m *Metrics) AddChatErrors(n int64)     { m.ChatErrors.Add(n) }
func (m *Metrics) AddToolCalls(n int64)      { m.ToolCalls.Add(n) }
func (m *Metrics) AddPermissionDeny(n int64) { m.PermissionDeny.Add(n) }
func (m *Metrics) AddMemoryWrites(n int64)   { m.MemoryWrites.Add(n) }
func (m *Metrics) AddMemoryReads(n int64)    { m.MemoryReads.Add(n) }
func (m *Metrics) AddTokens(n int64)         { m.TokensTotal.Add(n) }
func (m *Metrics) AddReflect(n int64)        { m.ReflectTotal.Add(n) }
func (m *Metrics) AddCompress(n int64)       { m.CompressTotal.Add(n) }
func (m *Metrics) AddBlobOffload(n int64)    { m.BlobOffload.Add(n) }
func (m *Metrics) AddEmbeddingCalls(n int64) { m.EmbeddingCalls.Add(n) }
func (m *Metrics) AddEmbeddingErrors(n int64) { m.EmbeddingErrs.Add(n) }

func (m *Metrics) ObserveLLM(d time.Duration) {
	if m == nil {
		return
	}
	m.LLMLatencySumMs.Add(d.Milliseconds())
	m.LLMLatencyCount.Add(1)
}

func (m *Metrics) ObserveTool(d time.Duration) {
	if m == nil {
		return
	}
	m.ToolLatencySumMs.Add(d.Milliseconds())
	m.ToolLatencyCount.Add(1)
}

func (m *Metrics) Snapshot() map[string]any {
	if m == nil {
		return map[string]any{}
	}
	llmN := m.LLMLatencyCount.Load()
	toolN := m.ToolLatencyCount.Load()
	var llmAvg, toolAvg int64
	if llmN > 0 {
		llmAvg = m.LLMLatencySumMs.Load() / llmN
	}
	if toolN > 0 {
		toolAvg = m.ToolLatencySumMs.Load() / toolN
	}
	return map[string]any{
		"chat_total": m.ChatTotal.Load(), "chat_errors": m.ChatErrors.Load(),
		"tool_calls": m.ToolCalls.Load(), "permission_deny": m.PermissionDeny.Load(),
		"memory_writes": m.MemoryWrites.Load(), "memory_reads": m.MemoryReads.Load(),
		"tokens_total": m.TokensTotal.Load(), "reflect_total": m.ReflectTotal.Load(),
		"compress_total": m.CompressTotal.Load(), "blob_offload_total": m.BlobOffload.Load(),
		"embedding_calls": m.EmbeddingCalls.Load(), "embedding_errors": m.EmbeddingErrs.Load(),
		"llm_latency_avg_ms": llmAvg, "llm_latency_count": llmN,
		"tool_latency_avg_ms": toolAvg, "tool_latency_count": toolN,
	}
}

// default process registry (also exposed as Global for transitional call sites).
var (
	defaultMetrics = NewMetrics()
	// Global is the default in-process metrics. Prefer Current() / SetMetrics for new code.
	// Kept as *Metrics so legacy field access (Global.ChatTotal.Add) still compiles during migration.
	Global = defaultMetrics
	metricsReg CounterRegistry = defaultMetrics
)

// SetMetrics installs the process-wide metrics registry (nil restores default).
// Mirrors domain/telemetry.Set for testability.
func SetMetrics(r CounterRegistry) {
	if r == nil {
		metricsReg = defaultMetrics
		Global = defaultMetrics
		return
	}
	metricsReg = r
	if m, ok := r.(*Metrics); ok {
		Global = m
	}
}

// Current returns the active CounterRegistry (never nil).
func Current() CounterRegistry {
	if metricsReg == nil {
		return defaultMetrics
	}
	return metricsReg
}
