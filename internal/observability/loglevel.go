package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// Log levels: debug=0 info=1 warn=2 error=3
var logLevel atomic.Int32

func init() {
	SetLogLevel("info")
}

func SetLogLevel(level string) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "trace":
		logLevel.Store(0)
	case "info", "":
		logLevel.Store(1)
	case "warn", "warning":
		logLevel.Store(2)
	case "error":
		logLevel.Store(3)
	default:
		logLevel.Store(1)
	}
	// Also sync slog level
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slogLevel(),
	})))
}

func slogLevel() slog.Level {
	switch logLevel.Load() {
	case 0:
		return slog.LevelDebug
	case 2:
		return slog.LevelWarn
	case 3:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func LogLevel() string {
	switch logLevel.Load() {
	case 0:
		return "debug"
	case 2:
		return "warn"
	case 3:
		return "error"
	default:
		return "info"
	}
}

// Debugf logs at debug level using slog.
func Debugf(format string, args ...any) {
	if logLevel.Load() <= 0 {
		slog.Default().Debug(format, args...)
	}
}

// Infof logs at info level using slog.
func Infof(format string, args ...any) {
	if logLevel.Load() <= 1 {
		slog.Default().Info(format, args...)
	}
}

// Warnf logs at warn level using slog.
func Warnf(format string, args ...any) {
	if logLevel.Load() <= 2 {
		slog.Default().Warn(format, args...)
	}
}

// Errorf logs at error level using slog.
func Errorf(format string, args ...any) {
	slog.Default().Error(format, args...)
}

// DebugContext logs at debug level with context.
func DebugContext(ctx context.Context, msg string, args ...any) {
	if logLevel.Load() <= 0 {
		slog.Default().DebugContext(ctx, msg, args...)
	}
}

// InfoContext logs at info level with context.
func InfoContext(ctx context.Context, msg string, args ...any) {
	if logLevel.Load() <= 1 {
		slog.Default().InfoContext(ctx, msg, args...)
	}
}

// WarnContext logs at warn level with context.
func WarnContext(ctx context.Context, msg string, args ...any) {
	if logLevel.Load() <= 2 {
		slog.Default().WarnContext(ctx, msg, args...)
	}
}

// ErrorContext logs at error level with context.
func ErrorContext(ctx context.Context, msg string, args ...any) {
	slog.Default().ErrorContext(ctx, msg, args...)
}

// ApplyFromEnv reads LOG_LEVEL.
func ApplyLogLevelFromEnv() {
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		SetLogLevel(v)
	}
}
