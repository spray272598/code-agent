package checkpoint

import (
	"context"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

// RunHandle is an in-process cancellable agent run.
type RunHandle struct {
	SessionID string
	Cancel    context.CancelFunc
	ControlCh chan engine.Control // optional mid-run instruction channel
	Started   time.Time
}

// RunRegistry tracks active agent loops for cross-request interrupt (cancel)
// and mid-run control (replan / pause / resume). Process-local only; durable
// state lives in Store.
type RunRegistry struct {
	mu   sync.Mutex
	runs map[string]*RunHandle
}

func NewRunRegistry() *RunRegistry {
	return &RunRegistry{runs: map[string]*RunHandle{}}
}

// Register associates a cancel func with session; previous cancel is invoked.
func (r *RunRegistry) Register(sessionID string, cancel context.CancelFunc) {
	if r == nil || sessionID == "" || cancel == nil {
		return
	}
	r.mu.Lock()
	if old, ok := r.runs[sessionID]; ok && old.Cancel != nil {
		old.Cancel()
	}
	r.runs[sessionID] = &RunHandle{SessionID: sessionID, Cancel: cancel, Started: time.Now()}
	r.mu.Unlock()
}

// Unregister removes the active handle for session (finish path).
func (r *RunRegistry) Unregister(sessionID string, _ context.CancelFunc) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	delete(r.runs, sessionID)
	r.mu.Unlock()
}

// AttachControl binds a control channel to an already-registered session so
// callers can deliver mid-run instructions. No-op when the session is absent.
func (r *RunRegistry) AttachControl(sessionID string, ch chan engine.Control) {
	if r == nil || sessionID == "" || ch == nil {
		return
	}
	r.mu.Lock()
	if h, ok := r.runs[sessionID]; ok && h != nil {
		h.ControlCh = ch
		// also clear the map entry's channel reference consistency
		r.runs[sessionID] = h
	}
	r.mu.Unlock()
}

// Cancel interrupts an active run; returns true if a handle existed.
func (r *RunRegistry) Cancel(sessionID string) bool {
	if r == nil || sessionID == "" {
		return false
	}
	r.mu.Lock()
	h, ok := r.runs[sessionID]
	if ok {
		delete(r.runs, sessionID)
	}
	r.mu.Unlock()
	if !ok || h == nil || h.Cancel == nil {
		return false
	}
	h.Cancel()
	return true
}

// IsRunning reports whether a session has an active in-process run.
func (r *RunRegistry) IsRunning(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	_, ok := r.runs[sessionID]
	r.mu.Unlock()
	return ok
}

// ActiveIDs returns the session IDs that currently have an active run.
func (r *RunRegistry) ActiveIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	ids := make([]string, 0, len(r.runs))
	for id := range r.runs {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	return ids
}

// ControlCh returns the control channel for an active session, or nil.
func (r *RunRegistry) ControlCh(sessionID string) chan engine.Control {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	h, ok := r.runs[sessionID]
	r.mu.Unlock()
	if !ok || h == nil {
		return nil
	}
	return h.ControlCh
}

// Control delivers a mid-run instruction to an active session. It silently
// no-ops when the session is not running or has no control channel. Returns
// true if the signal was delivered.
func (r *RunRegistry) Control(sessionID string, sig engine.ControlSignal, goal string) bool {
	ch := r.ControlCh(sessionID)
	if ch == nil {
		return false
	}
	select {
	case ch <- engine.Control{Signal: sig, Goal: goal}:
		return true
	default:
		// channel full: drop to avoid blocking the caller
		return false
	}
}

// Active lists session IDs currently running.
func (r *RunRegistry) Active() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.runs))
	for id := range r.runs {
		out = append(out, id)
	}
	return out
}
