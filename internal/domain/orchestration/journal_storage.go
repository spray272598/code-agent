package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JournalStorage is the persistence backend for Journal entries.
// Implementations: file (FileJournalStorage), MySQL (MySQLJournalStorage), Redis (RedisJournalStorage).
type JournalStorage interface {
	// Append writes a single journal entry atomically.
	Append(entry JournalEntry) error
	// ReadAll returns all entries for the given runID, ordered by timestamp.
	ReadAll(runID string) ([]JournalEntry, error)
	// Close releases any resources held by the storage backend.
	Close() error
}

// FileJournalStorage stores journal entries as append-only JSONL files.
// This is the default backend, suitable for single-instance deployments.
type FileJournalStorage struct {
	mu    sync.Mutex
	path  string
	file  *os.File
}

// NewFileJournalStorage opens (or creates) a journal file at dir/runID/journal.jsonl.
func NewFileJournalStorage(dir, runID string) (*FileJournalStorage, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	runDir := filepath.Join(dir, "workflows", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("create journal dir: %w", err)
	}
	path := filepath.Join(runDir, "journal.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open journal file: %w", err)
	}
	return &FileJournalStorage{path: path, file: f}, nil
}

func (s *FileJournalStorage) Append(entry JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	entry.Timestamp = time.Now()
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	if _, err := s.file.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	return s.file.Sync()
}

func (s *FileJournalStorage) ReadAll(runID string) ([]JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read journal file: %w", err)
	}
	var entries []JournalEntry
	for _, line := range splitLines(data) {
		if line == "" {
			continue
		}
		var e JournalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *FileJournalStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// Path returns the journal file path (empty for ephemeral storage).
func (s *FileJournalStorage) Path() string {
	return s.path
}

// MemoryJournalStorage is an in-memory-only journal storage (no persistence).
// Useful for tests and short-lived workflows.
type MemoryJournalStorage struct {
	mu      sync.Mutex
	entries map[string][]JournalEntry // runID → entries
}

func NewMemoryJournalStorage() *MemoryJournalStorage {
	return &MemoryJournalStorage{entries: map[string][]JournalEntry{}}
}

func (s *MemoryJournalStorage) Append(entry JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.Timestamp = time.Now()
	s.entries[entry.RunID] = append(s.entries[entry.RunID], entry)
	return nil
}

func (s *MemoryJournalStorage) ReadAll(runID string) ([]JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[runID], nil
}

func (s *MemoryJournalStorage) Close() error { return nil }