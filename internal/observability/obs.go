package observability

import (
	"context"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type ctxKey string

const RequestIDKey ctxKey = "request_id"

// Metrics process-local counters + latency aggregates (ms).
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

	// latency sum/count for averages
	LLMLatencySumMs  atomic.Int64
	LLMLatencyCount  atomic.Int64
	ToolLatencySumMs atomic.Int64
	ToolLatencyCount atomic.Int64
}

var Global = &Metrics{}

func (m *Metrics) ObserveLLM(d time.Duration) {
	m.LLMLatencySumMs.Add(d.Milliseconds())
	m.LLMLatencyCount.Add(1)
}

func (m *Metrics) ObserveTool(d time.Duration) {
	m.ToolLatencySumMs.Add(d.Milliseconds())
	m.ToolLatencyCount.Add(1)
}

func (m *Metrics) Snapshot() map[string]any {
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
		"compress_total": m.CompressTotal.Load(),
		"llm_latency_avg_ms": llmAvg, "llm_latency_count": llmN,
		"tool_latency_avg_ms": toolAvg, "tool_latency_count": toolN,
	}
}

type Logger struct {
	mu sync.Mutex
}

func (l *Logger) Event(fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	log.Printf("[trace] %v\n", fields)
}

var Trace = &Logger{}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = time.Now().Format("20060102T150405.000000")
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), RequestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey).(string); ok {
		return v
	}
	return ""
}

func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[http] %s %s rid=%s dur=%v\n", r.Method, r.URL.Path, RequestID(r.Context()), time.Since(start))
	})
}
