// Package logger provides structured logging via log/slog with context propagation.
// Domain and infrastructure code should use this package instead of log.Printf.
//
// Architecture:
//   - Domain code: use logger.FromContext(ctx) or package-level Info/Warn/Error
//   - Infrastructure code: use logger.With("key", "value") for structured fields
//   - Bootstrap: call logger.Init() once at startup
package logger

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

// Init sets up the global slog handler. Call once at startup.
// level: "debug", "info", "warn", "error"
// format: "json" or "text"
func Init(level, format string) {
	var h slog.Handler
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// With returns a new logger with the given key-value pairs attached.
// Use this to add structured context to log statements.
func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}

// FromContext extracts a logger from the context. Falls back to the default logger.
// Context should carry request_id, session_id, user_id etc. via ContextWithLogger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// ContextWithLogger stores a logger in the context.
func ContextWithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// WithContext returns a logger enriched with context values (request_id, session_id, etc).
// This is a convenience for handlers that receive a context.
func WithContext(ctx context.Context, args ...any) *slog.Logger {
	l := FromContext(ctx)
	if len(args) > 0 {
		l = l.With(args...)
	}
	return l
}

// --- Package-level convenience (short call sites) ---

func Debug(msg string, args ...any) { slog.Default().Debug(msg, args...) }
func Info(msg string, args ...any)  { slog.Default().Info(msg, args...) }
func Warn(msg string, args ...any)  { slog.Default().Warn(msg, args...) }
func Error(msg string, args ...any) { slog.Default().Error(msg, args...) }

// DebugContext logs at debug level with context.
func DebugContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).Debug(msg, args...)
}

// InfoContext logs at info level with context.
func InfoContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).Info(msg, args...)
}

// WarnContext logs at warn level with context.
func WarnContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).Warn(msg, args...)
}

// ErrorContext logs at error level with context.
func ErrorContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).Error(msg, args...)
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug", "trace":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
