package hook

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
)

// Point in agent lifecycle (walicode/Claude-style).
type Point string

const (
	SessionStart Point = "SessionStart"
	SessionEnd   Point = "SessionEnd"
	PreToolUse   Point = "PreToolUse"
	PostToolUse  Point = "PostToolUse"
	PreCompact   Point = "PreCompact"
	Permission   Point = "PermissionDecision"
)

// ErrAbort is returned by handlers to cancel tool execution (Claude Code PreToolUse abort).
var ErrAbort = errors.New("hook abort")

// Abort wraps a reason as a sentinel abort error.
func Abort(reason string) error {
	if reason == "" {
		return ErrAbort
	}
	return fmt.Errorf("%w: %s", ErrAbort, reason)
}

func IsAbort(err error) bool {
	return err != nil && errors.Is(err, ErrAbort)
}

type Event struct {
	Point     Point
	SessionID string
	Tool      string
	Args      map[string]any
	Result    string
	Decision  string
	Meta      map[string]any
}

type Handler func(ctx context.Context, ev Event) error

// Bus runs registered handlers.
// PreToolUse: first Abort error cancels the tool.
// Other points: errors are logged only.
type Bus struct {
	mu       sync.RWMutex
	handlers map[Point][]Handler
}

func NewBus() *Bus {
	return &Bus{handlers: map[Point][]Handler{}}
}

func (b *Bus) On(point Point, h Handler) {
	if h == nil {
		return
	}
	b.mu.Lock()
	b.handlers[point] = append(b.handlers[point], h)
	b.mu.Unlock()
}

// Emit runs handlers; non-abort errors are logged. Does not abort.
func (b *Bus) Emit(ctx context.Context, ev Event) {
	_, _ = b.EmitCheck(ctx, ev)
}

// EmitCheck runs handlers. For PreToolUse, returns abort error if any handler aborts.
func (b *Bus) EmitCheck(ctx context.Context, ev Event) (aborted bool, err error) {
	if b == nil {
		return false, nil
	}
	b.mu.RLock()
	hs := append([]Handler{}, b.handlers[ev.Point]...)
	b.mu.RUnlock()
	for _, h := range hs {
		if e := h(ctx, ev); e != nil {
			if IsAbort(e) {
				return true, e
			}
			log.Printf("[hook] %s error: %v\n", ev.Point, e)
		}
	}
	return false, nil
}

// DefaultLogger registers a debug logger for all points.
func (b *Bus) RegisterDefaultLogger() {
	logH := func(ctx context.Context, ev Event) error {
		log.Printf("[hook] %s session=%s tool=%s\n", ev.Point, ev.SessionID, ev.Tool)
		return nil
	}
	for _, p := range []Point{SessionStart, SessionEnd, PreToolUse, PostToolUse, PreCompact, Permission} {
		b.On(p, logH)
	}
}
