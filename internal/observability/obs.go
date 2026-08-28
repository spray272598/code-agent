package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// LogError logs an operation error with structured fields.
func LogError(op string, err error) {
	slog.Default().Error("operation failed", "op", op, "error", err)
}

// LogErrorf logs an operation error with a format string.
func LogErrorf(op, format string, args ...any) {
	slog.Default().Error("operation failed: "+format, append([]any{"op", op}, args...)...)
}

// TraceEvent logs a structured trace event.
func TraceEvent(fields map[string]any) {
	slog.Default().LogAttrs(context.Background(), slog.LevelDebug, "trace event",
		slog.Any("data", fields),
	)
}

// RequestID is a convenience for extracting the request ID from context.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(requestIDKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

type requestIDKey struct{}

// ContextWithRequestID stores the request ID in context.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDMiddleware generates a unique request ID and stores it in context.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		ctx := ContextWithRequestID(r.Context(), id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Trace is a helper for event logging via the ObservabilityBridge.
var Trace = &traceHelper{}

type traceHelper struct{}

func (t *traceHelper) Event(fields map[string]any) {
	TraceEvent(fields)
}

// AccessLog returns middleware that logs HTTP requests with structured fields.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Default().Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", RequestID(r.Context()),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
