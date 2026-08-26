// Package checkpoint provides cross-process interrupt state and active-run control.
// HITL pending tools and cancelled runs survive process restart when using FileStore.
package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status of a run snapshot.
const (
	StatusRunning   = "running"
	StatusInterrupt = "interrupt" // HITL / Eino interrupt awaiting approval
	StatusCancelled = "cancelled"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// PendingTool mirrors security.PendingConfirm for JSON durability (no domain cycle).
type PendingTool struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	RuleID    string         `json:"ruleId,omitempty"`
	Layer     string         `json:"layer,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Snapshot is durable agent run state for resume after restart.
type Snapshot struct {
	SessionID    string         `json:"sessionId"`
	UserID       string         `json:"userId,omitempty"`
	ProjectID    string         `json:"projectId,omitempty"`
	Status       string         `json:"status"`
	Goal         string         `json:"goal,omitempty"`
	LastInput    string         `json:"lastInput,omitempty"`
	Step         int            `json:"step,omitempty"`
	Orchestrator string         `json:"orchestrator,omitempty"`
	Pending      *PendingTool   `json:"pending,omitempty"`
	ErrorClass   string         `json:"errorClass,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// Store is durable checkpoint persistence.
type Store interface {
	Save(ctx context.Context, s *Snapshot) error
	Get(ctx context.Context, sessionID string) (*Snapshot, error)
	Delete(ctx context.Context, sessionID string) error
	List(ctx context.Context, status string, limit int) ([]*Snapshot, error)
}

// MemoryStore for tests / ephemeral demos.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]*Snapshot
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: map[string]*Snapshot{}}
}

func (m *MemoryStore) Save(_ context.Context, s *Snapshot) error {
	if s == nil || s.SessionID == "" {
		return fmt.Errorf("invalid snapshot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	if s.Pending != nil {
		p := *s.Pending
		cp.Pending = &p
	}
	cp.UpdatedAt = time.Now()
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = cp.UpdatedAt
	}
	m.data[s.SessionID] = &cp
	return nil
}

func (m *MemoryStore) Get(_ context.Context, sessionID string) (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.data[sessionID]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (m *MemoryStore) Delete(_ context.Context, sessionID string) error {
	m.mu.Lock()
	delete(m.data, sessionID)
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) List(_ context.Context, status string, limit int) ([]*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Snapshot
	for _, s := range m.data {
		if status != "" && s.Status != status {
			continue
		}
		cp := *s
		out = append(out, &cp)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// FileStore persists one JSON file per session under Dir.
type FileStore struct {
	Dir string
	mu  sync.Mutex
}

func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		dir = "./data/checkpoints"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{Dir: dir}, nil
}

func (f *FileStore) path(sessionID string) string {
	// sanitize session id for filename
	safe := filepath.Base(sessionID)
	return filepath.Join(f.Dir, safe+".json")
}

func (f *FileStore) Save(_ context.Context, s *Snapshot) error {
	if s == nil || s.SessionID == "" {
		return fmt.Errorf("invalid snapshot")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s.UpdatedAt = time.Now()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path(s.SessionID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path(s.SessionID))
}

func (f *FileStore) Get(_ context.Context, sessionID string) (*Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := os.ReadFile(f.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (f *FileStore) Delete(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := os.Remove(f.path(sessionID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (f *FileStore) List(_ context.Context, status string, limit int) ([]*Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Snapshot
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(f.Dir, e.Name()))
		if err != nil {
			continue
		}
		var s Snapshot
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if status != "" && s.Status != status {
			continue
		}
		out = append(out, &s)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
