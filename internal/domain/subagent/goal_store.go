// Experimental: part of the GoalOrchestrator subsystem (plan→execute→verify).
// Not wired into the default agent runtime yet; treat as a spike, API may churn.
package subagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// GoalStore provides persistence for GoalTracker snapshots, enabling
// goal-driven sessions to survive restarts and be inspected later.
// Inspired by DeepSeek Harness's session-persistent goal management.
type GoalStore struct {
	mu        sync.RWMutex
	dir       string
	active    map[string]*GoalSnapshot // in-memory active goals
	listeners []func(event GoalEvent, snapshot *GoalSnapshot)
}

// NewGoalStore creates a goal store that persists to the given directory.
func NewGoalStore(dir string) (*GoalStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create goal store dir: %w", err)
	}
	gs := &GoalStore{
		dir:    dir,
		active: make(map[string]*GoalSnapshot),
	}
	// Load existing active goals from disk
	if err := gs.loadAll(); err != nil {
		return nil, err
	}
	return gs, nil
}

// Save persists a goal snapshot to disk and in-memory cache.
func (gs *GoalStore) Save(snapshot *GoalSnapshot) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.active[snapshot.ID] = snapshot
	return gs.persist(snapshot)
}

// Get retrieves a goal snapshot by ID.
func (gs *GoalStore) Get(id string) (*GoalSnapshot, bool) {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	s, ok := gs.active[id]
	return s, ok
}

// List returns all active goal snapshots.
func (gs *GoalStore) List() []*GoalSnapshot {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	out := make([]*GoalSnapshot, 0, len(gs.active))
	for _, s := range gs.active {
		out = append(out, s)
	}
	return out
}

// Archive moves a completed goal from active to archived on disk.
func (gs *GoalStore) Archive(id string) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	snapshot, ok := gs.active[id]
	if !ok {
		return fmt.Errorf("goal %s not found", id)
	}
	// Write to archive directory
	archiveDir := filepath.Join(gs.dir, "archive")
	os.MkdirAll(archiveDir, 0o755)
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(archiveDir, id+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	// Remove from active
	delete(gs.active, id)
	os.Remove(filepath.Join(gs.dir, id+".json"))
	return nil
}

// Remove deletes a goal from active store.
func (gs *GoalStore) Remove(id string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	delete(gs.active, id)
	os.Remove(filepath.Join(gs.dir, id+".json"))
}

// OnEvent registers a listener for goal lifecycle events.
func (gs *GoalStore) OnEvent(fn func(event GoalEvent, snapshot *GoalSnapshot)) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	gs.listeners = append(gs.listeners, fn)
}

// EmitEvent notifies all listeners of a goal event and persists the snapshot.
func (gs *GoalStore) EmitEvent(event GoalEvent, snapshot *GoalSnapshot) {
	gs.mu.RLock()
	listeners := make([]func(GoalEvent, *GoalSnapshot), len(gs.listeners))
	copy(listeners, gs.listeners)
	gs.mu.RUnlock()

	for _, fn := range listeners {
		fn(event, snapshot)
	}
}

// SnapshotFromTracker creates a GoalSnapshot from a GoalTracker's current state.
func SnapshotFromTracker(tracker *GoalTracker) *GoalSnapshot {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return &GoalSnapshot{
		ID:                  tracker.id,
		Objective:           tracker.objective,
		Status:              tracker.status,
		Phase:               tracker.phase,
		TokenBudget:         tracker.tokenBudget,
		TokensUsed:          tracker.tokensUsed,
		TotalWorkerRounds:   tracker.totalWorkers,
		TotalVerifyRounds:   tracker.verifierCount,
		CreatedAt:           tracker.createdAt,
		UpdatedAt:           time.Now(),
		ConsecutiveFailures: tracker.consecutiveFailures,
	}
}

func (gs *GoalStore) persist(snapshot *GoalSnapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(gs.dir, snapshot.ID+".json")
	return os.WriteFile(path, data, 0o644)
}

func (gs *GoalStore) loadAll() error {
	entries, err := os.ReadDir(gs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(gs.dir, entry.Name()))
		if err != nil {
			continue
		}
		var snapshot GoalSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			continue
		}
		gs.active[snapshot.ID] = &snapshot
	}
	return nil
}
