package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type CheckpointEntry struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	UserID    string    `json:"user_id"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
}

type DurableCheckpointStore struct {
	mu      sync.RWMutex
	dir     string
	cap     int
	cache   map[string][]CheckpointEntry
}

func NewDurableCheckpointStore(dir string, cap int) (*DurableCheckpointStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create checkpoint dir: %w", err)
	}
	s := &DurableCheckpointStore{
		dir:   dir,
		cap:   cap,
		cache: make(map[string][]CheckpointEntry),
	}
	if err := s.rehydrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DurableCheckpointStore) rehydrate() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ce, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var entry CheckpointEntry
		if err := json.Unmarshal(ce, &entry); err != nil {
			continue
		}
		s.cache[entry.SessionID] = append(s.cache[entry.SessionID], entry)
	}
	for sid := range s.cache {
		sort.Slice(s.cache[sid], func(i, j int) bool {
			return s.cache[sid][i].ID < s.cache[sid][j].ID
		})
		if len(s.cache[sid]) > s.cap {
			s.cache[sid] = s.cache[sid][len(s.cache[sid])-s.cap:]
		}
	}
	return nil
}

func (s *DurableCheckpointStore) Save(ctx context.Context, entry *CheckpointEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache[entry.SessionID] = append(s.cache[entry.SessionID], *entry)
	sort.Slice(s.cache[entry.SessionID], func(i, j int) bool {
		return s.cache[entry.SessionID][i].ID < s.cache[entry.SessionID][j].ID
	})
	if len(s.cache[entry.SessionID]) > s.cap {
		s.cache[entry.SessionID] = s.cache[entry.SessionID][len(s.cache[entry.SessionID])-s.cap:]
	}

	path := filepath.Join(s.dir, fmt.Sprintf("checkpoint-%s-%d.json", entry.SessionID, entry.ID))
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (s *DurableCheckpointStore) List(ctx context.Context, sessionID string) ([]CheckpointEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[sessionID], nil
}

func (s *DurableCheckpointStore) Latest(ctx context.Context, sessionID string) (*CheckpointEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.cache[sessionID]
	if len(entries) == 0 {
		return nil, nil
	}
	return &entries[len(entries)-1], nil
}

func (s *DurableCheckpointStore) Rewind(ctx context.Context, sessionID string, targetID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.cache[sessionID]
	var kept []CheckpointEntry
	for _, e := range entries {
		if e.ID <= targetID {
			kept = append(kept, e)
		}
	}
	s.cache[sessionID] = kept

	for _, e := range entries {
		if e.ID > targetID {
			path := filepath.Join(s.dir, fmt.Sprintf("checkpoint-%s-%d.json", sessionID, e.ID))
			os.Remove(path)
		}
	}
	return nil
}

func (s *DurableCheckpointStore) TruncateFrom(ctx context.Context, sessionID string, fromID int64) error {
	return s.Rewind(ctx, sessionID, fromID-1)
}

func (s *DurableCheckpointStore) Prune(ctx context.Context, olderThan time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pruned := 0
	for sid, entries := range s.cache {
		var kept []CheckpointEntry
		for _, e := range entries {
			if e.CreatedAt.Before(olderThan) {
				path := filepath.Join(s.dir, fmt.Sprintf("checkpoint-%s-%d.json", sid, e.ID))
				os.Remove(path)
				pruned++
			} else {
				kept = append(kept, e)
			}
		}
		s.cache[sid] = kept
	}
	return pruned, nil
}

func (s *DurableCheckpointStore) Sessions(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sessions []string
	for sid := range s.cache {
		sessions = append(sessions, sid)
	}
	return sessions, nil
}

func (s *DurableCheckpointStore) Clear(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.cache[sessionID]
	for _, e := range entries {
		path := filepath.Join(s.dir, fmt.Sprintf("checkpoint-%s-%d.json", sessionID, e.ID))
		os.Remove(path)
	}
	delete(s.cache, sessionID)
	return nil
}

func (s *DurableCheckpointStore) ListByUser(ctx context.Context, userID string, limit int) ([]CheckpointEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var results []CheckpointEntry
	for _, entries := range s.cache {
		for _, e := range entries {
			if e.UserID == userID {
				results = append(results, e)
			}
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

var _ = memport.ScopeUser
