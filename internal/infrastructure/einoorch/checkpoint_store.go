package einoorch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func init() {
	// ConfirmInfo must be gob-serializable for StatefulInterrupt state persistence.
	schema.RegisterName[ConfirmInfo]("code_agent_confirm_info")
}

// MemoryCheckPointStore is an in-process compose.CheckPointStore.
type MemoryCheckPointStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func NewMemoryCheckPointStore() *MemoryCheckPointStore {
	return &MemoryCheckPointStore{m: make(map[string][]byte)}
}

func (s *MemoryCheckPointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[id]
	if !ok {
		return nil, false, nil
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, true, nil
}

func (s *MemoryCheckPointStore) Set(_ context.Context, id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	s.m[id] = cp
	return nil
}

// FileCheckPointStore persists Eino graph checkpoints under a directory.
type FileCheckPointStore struct {
	dir string
	mu  sync.Mutex
}

func NewFileCheckPointStore(dir string) (*FileCheckPointStore, error) {
	if dir == "" {
		dir = "./data/eino-checkpoints"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileCheckPointStore{dir: dir}, nil
}

func (s *FileCheckPointStore) path(id string) string {
	return filepath.Join(s.dir, safeFileID(id)+".bin")
}

func safeFileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "empty"
	}
	id = filepath.Base(id)
	id = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, id)
	if len(id) > 180 {
		id = id[:180]
	}
	return id
}

func (s *FileCheckPointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}

func (s *FileCheckPointStore) Set(_ context.Context, id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(id))
}

// Ensure compile-time interface satisfaction.
var (
	_ compose.CheckPointStore = (*MemoryCheckPointStore)(nil)
	_ compose.CheckPointStore = (*FileCheckPointStore)(nil)
)

// DefaultGraphCheckPointID returns the Eino checkpoint id for a session.
func DefaultGraphCheckPointID(sessionID string) string {
	if sessionID == "" {
		return "eino-anon"
	}
	return "eino-" + sessionID
}

// GraphInterruptMeta keys stored in domain checkpoint Meta for graph resume.
const (
	MetaGraphInterruptID = "eino_interrupt_id"
	MetaGraphCheckPoint  = "eino_checkpoint_id"
	MetaGraphResume      = "eino_graph_resume"
)

// ExtractFirstInterruptID pulls the first interrupt context id from an error.
func ExtractFirstInterruptID(err error) string {
	if err == nil {
		return ""
	}
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok || info == nil || len(info.InterruptContexts) == 0 {
		return ""
	}
	return info.InterruptContexts[0].ID
}

// MustHaveStore documents requirement for graph resume.
func MustHaveStore(store compose.CheckPointStore) error {
	if store == nil {
		return fmt.Errorf("graph checkpoint store not configured")
	}
	return nil
}
