package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Entry struct {
	Seq     uint64          `json:"seq"`
	Kind    string          `json:"kind"`
	ReqHash string          `json:"reqHash"`
	Result  json.RawMessage `json:"result"`
}

type Journal struct {
	mu      sync.Mutex
	entries map[uint64]Entry
	path    string
}

func NewJournal(path string) (*Journal, error) {
	j := &Journal{
		entries: map[uint64]Entry{},
		path:    path,
	}
	if path == "" {
		return j, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return j, nil
		}
		return nil, fmt.Errorf("journal: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return j, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("journal: unmarshal %s: %w", path, err)
	}
	for _, e := range entries {
		j.entries[e.Seq] = e
	}
	return j, nil
}

func (j *Journal) Record(seq uint64, kind string, reqHash string, result map[string]any) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("journal: marshal result: %w", err)
	}
	j.entries[seq] = Entry{
		Seq:     seq,
		Kind:    kind,
		ReqHash: reqHash,
		Result:  json.RawMessage(b),
	}
	return nil
}

func (j *Journal) Replay(seq uint64, kind string, reqHash string) (map[string]any, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	e, ok := j.entries[seq]
	if !ok {
		return nil, false
	}
	if e.Kind != kind || e.ReqHash != reqHash {
		return nil, false
	}
	var result map[string]any
	if err := json.Unmarshal(e.Result, &result); err != nil {
		return nil, false
	}
	return result, true
}

func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries)
}

func (j *Journal) Save() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path == "" {
		return nil
	}
	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("journal: mkdir %s: %w", dir, err)
	}
	entries := make([]Entry, 0, len(j.entries))
	for _, e := range j.entries {
		entries = append(entries, e)
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("journal: marshal: %w", err)
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("journal: write %s: %w", tmp, err)
	}
	return os.Rename(tmp, j.path)
}

func ComputeHash(kind string, payload map[string]any) string {
	h := sha256.New()
	h.Write([]byte(kind))
	b, _ := json.Marshal(payload)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}