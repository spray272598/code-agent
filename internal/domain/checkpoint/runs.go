package checkpoint

import (
	"context"
	"sync"
	"time"
)

// RunHandle is an in-process cancellable agent run.
type RunHandle struct {
	SessionID string
	Cancel    context.CancelFunc
	Started   time.Time
}

// RunRegistry tracks active agent loops for cross-request interrupt (cancel).
// Process-local only; durable state lives in Store.
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
