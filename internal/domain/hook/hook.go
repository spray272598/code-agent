package hook

import (
	"context"
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

// Bus runs registered handlers. Failures are logged; abort=false by default.
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

func (b *Bus) Emit(ctx context.Context, ev Event) {
	if b == nil {
		return
	}
	b.mu.RLock()
	hs := append([]Handler{}, b.handlers[ev.Point]...)
	b.mu.RUnlock()
	for _, h := range hs {
		if err := h(ctx, ev); err != nil {
			log.Printf("[hook] %s error: %v\n", ev.Point, err)
		}
	}
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
