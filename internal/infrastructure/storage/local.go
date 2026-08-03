package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LocalStore filesystem blob store under root (default ./data/objects).
type LocalStore struct {
	root string
	mu   sync.Mutex
}

func NewLocalStore(root string) (*LocalStore, error) {
	if root == "" {
		root = "./data/objects"
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &LocalStore{root: abs}, nil
}

func (s *LocalStore) pathFor(key string) (string, error) {
	key = strings.TrimPrefix(filepath.ToSlash(key), "/")
	if key == "" || strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid key")
	}
	full := filepath.Join(s.root, filepath.FromSlash(key))
	// ensure under root
	rel, err := filepath.Rel(s.root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("key escapes root")
	}
	return full, nil
}

func (s *LocalStore) Put(_ context.Context, key string, data []byte, contentType string) error {
	_ = contentType
	s.mu.Lock()
	defer s.mu.Unlock()
	full, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

func (s *LocalStore) Get(_ context.Context, key string) ([]byte, error) {
	full, err := s.pathFor(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

func (s *LocalStore) Exists(_ context.Context, key string) bool {
	full, err := s.pathFor(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(full)
	return err == nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	full, err := s.pathFor(key)
	if err != nil {
		return err
	}
	return os.Remove(full)
}

func (s *LocalStore) SignURL(_ context.Context, key string, expireSec int) (string, error) {
	_ = expireSec
	// local: return path hint
	full, err := s.pathFor(key)
	if err != nil {
		return "", err
	}
	return "file://" + filepath.ToSlash(full), nil
}

func (s *LocalStore) Root() string { return s.root }
