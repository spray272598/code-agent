package observability

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

type ctxKey string

const RequestIDKey ctxKey = "request_id"

// LogError records a failed side-effect that must not abort the user-facing path
// (persist, audit, checkpoint). Prefer this over silently ignoring errors.
func LogError(op string, err error) {
	if err == nil {
		return
	}
	log.Printf("[error] %s: %v\n", op, err)
}

// LogErrorf formats and logs a non-fatal operational error.
func LogErrorf(format string, args ...any) {
	log.Printf("[error] "+format+"\n", args...)
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
