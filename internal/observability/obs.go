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

// Metrics simple in-process counters (Phase 3 light observability).
type Metrics struct {
	ChatTotal      atomic.Int64
	ChatErrors     atomic.Int64
	ToolCalls      atomic.Int64
	PermissionDeny atomic.Int64
	MemoryWrites   atomic.Int64
	MemoryReads    atomic.Int64
	TokensTotal    atomic.Int64
}

var Global = &Metrics{}

func (m *Metrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"chat_total": m.ChatTotal.Load(), "chat_errors": m.ChatErrors.Load(),
		"tool_calls": m.ToolCalls.Load(), "permission_deny": m.PermissionDeny.Load(),
		"memory_writes": m.MemoryWrites.Load(), "memory_reads": m.MemoryReads.Load(),
		"tokens_total": m.TokensTotal.Load(),
	}
}

// Logger structured-ish step logger for agent traces.
type Logger struct {
	mu sync.Mutex
}

func (l *Logger) Event(fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	log.Printf("[trace] %v\n", fields)
}

var Trace = &Logger{}

// RequestIDMiddleware injects X-Request-Id.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = time.Now().Format("20060102T150405.000") + "-" + rand3()
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

func rand3() string {
	return time.Now().Format("000")
}

// AccessLog minimal HTTP access log.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[http] %s %s %s %v\n", r.Method, r.URL.Path, RequestID(r.Context()), time.Since(start))
	})
}
