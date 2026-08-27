package einoorch

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/security"
)

// controlHandler drains ControlCh concurrently during an Eino agent run.
// Since handle.Generate() runs the entire ReAct loop internally, we cannot
// interrupt it mid-step. Instead, we:
//  1. Drain ControlCh in a background goroutine
//  2. Set atomic flags for non-blocking signals (replan, plan-explore, plan-implement)
//  3. Handle blocking signals (pause/resume) by parking until resumed
//  4. Cancel context on interrupt
//
// Callback handlers check the flags at model/tool boundaries.
type controlHandler struct {
	controlCh <-chan engine.Control
	perm      *security.Guard
	publish   EventSink
	ctx       context.Context
	cancelFn  context.CancelFunc

	interrupt   atomic.Bool
	replan      atomic.Bool
	planExplore atomic.Bool
	newGoal     atomic.Value

	mu     sync.Mutex
	paused bool
	resume chan struct{}
}

func newControlHandler(ch <-chan engine.Control, perm *security.Guard, publish EventSink, cancel context.CancelFunc) *controlHandler {
	h := &controlHandler{
		controlCh: ch,
		perm:      perm,
		publish:   publish,
		resume:    make(chan struct{}, 1),
		cancelFn:  cancel,
	}
	return h
}

// Start drains ControlCh in a goroutine. Call stop() to terminate.
func (h *controlHandler) Start(ctx context.Context) (stop func()) {
	if h.controlCh == nil {
		return func() {}
	}
	h.ctx = ctx
	ctx, cancel := context.WithCancel(ctx)
	h.cancelFn = cancel
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case c, ok := <-h.controlCh:
				if !ok {
					return
				}
				h.handle(c)
			}
		}
	}()
	return func() {
		h.cancelFn()
		<-done
	}
}

func (h *controlHandler) handle(c engine.Control) {
	switch c.Signal {
	case engine.ControlInterrupt:
		h.interrupt.Store(true)
		h.cancelFn()

	case engine.ControlReplan:
		h.replan.Store(true)

	case engine.ControlReplanWithGoal:
		h.replan.Store(true)
		h.newGoal.Store(strings.TrimSpace(c.Goal))

	case engine.ControlPause:
		h.publish(&engine.Event{
			Type: engine.EventCheckpoint, SubType: "paused",
			Content: "paused for input", Timestamp: nowMs(),
		})
		h.mu.Lock()
		h.paused = true
		h.mu.Unlock()
		// Block until resume, interrupt, or ctx cancel
		select {
		case <-h.resume:
			h.publish(&engine.Event{
				Type: engine.EventResume, Content: "resumed", Timestamp: nowMs(),
			})
		case <-h.ctx.Done():
		}

	case engine.ControlResume:
		h.mu.Lock()
		if h.paused {
			h.paused = false
			select {
			case h.resume <- struct{}{}:
			default:
			}
		}
		h.mu.Unlock()

	case engine.ControlPlanExplore:
		h.planExplore.Store(true)
		if h.perm != nil {
			h.perm.SetMode(security.ModeReadonly)
		}
		h.publish(&engine.Event{
			Type: engine.EventCheckpoint, SubType: "plan_explore",
			Content: "plan explore phase (read-only)", Timestamp: nowMs(),
		})

	case engine.ControlPlanImplement:
		h.planExplore.Store(false)
		if h.perm != nil {
			h.perm.SetMode(security.ModeWorkspace)
		}
		h.publish(&engine.Event{
			Type: engine.EventCheckpoint, SubType: "plan_implement",
			Content: "plan implement phase (writable)", Timestamp: nowMs(),
		})
	}
}

// ShouldInterrupt returns true if an interrupt signal was received.
func (h *controlHandler) ShouldInterrupt() bool { return h.interrupt.Load() }

// TakeReplan returns true if a replan was requested, and resets the flag.
// If a new goal was provided, it is returned.
func (h *controlHandler) TakeReplan() (bool, string) {
	if !h.replan.Swap(false) {
		return false, ""
	}
	if v := h.newGoal.Swap(""); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return true, s
		}
	}
	return true, ""
}

// IsPaused returns true if the handler is in a paused state.
func (h *controlHandler) IsPaused() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paused
}
