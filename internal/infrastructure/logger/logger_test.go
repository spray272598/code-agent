package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestContextLoggerRoundTrip(t *testing.T) {
	base := slog.Default()
	l := slog.New(slog.NewTextHandler(discard{}, nil))

	// no logger in context -> falls back to slog.Default
	if got := FromContext(context.Background()); got != base {
		t.Fatal("FromContext should return slog.Default when unset")
	}

	ctx := ContextWithLogger(context.Background(), l)
	if got := FromContext(ctx); got != l {
		t.Fatal("ContextWithLogger/FromContext round-trip mismatch")
	}

	// WithContext derives a non-nil logger carrying extra args
	derived := WithContext(ctx, "key", "val")
	if derived == nil {
		t.Fatal("WithContext returned nil")
	}
	// derived still resolves via context
	if got := FromContext(ContextWithLogger(context.Background(), derived)); got != derived {
		t.Fatal("derived logger not resolvable from context")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error)              { return len(p), nil }
func (discard) Enabled(context.Context, slog.Level) bool { return false }
