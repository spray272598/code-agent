package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ToolCallRecord captures a single tool invocation for snapshot testing.
type ToolCallRecord struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Result Result         `json:"result"`
}

// Snapshot captures the full behavioral trace of a tool execution sequence.
type Snapshot struct {
	Name      string           `json:"name"`
	ToolCalls []ToolCallRecord `json:"tool_calls"`
}

// SnapshotRecorder captures tool executions for golden file generation.
type SnapshotRecorder struct {
	mu    sync.Mutex
	calls []ToolCallRecord
	name  string
}

// NewSnapshotRecorder creates a recorder for the given test case name.
func NewSnapshotRecorder(name string) *SnapshotRecorder {
	return &SnapshotRecorder{name: name}
}

// Record captures a tool execution.
func (r *SnapshotRecorder) Record(name string, args map[string]any, result Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ToolCallRecord{Name: name, Args: args, Result: result})
}

// Snapshot returns the captured trace.
func (r *SnapshotRecorder) Snapshot() *Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Snapshot{Name: r.name, ToolCalls: append([]ToolCallRecord(nil), r.calls...)}
}

// Save writes the snapshot as a golden file under testdata/snapshots/.
func (r *SnapshotRecorder) Save(dir string) error {
	s := r.Snapshot()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, r.name+".json")
	return os.WriteFile(path, data, 0o644)
}

// LoadSnapshot reads a golden snapshot file.
func LoadSnapshot(dir, name string) (*Snapshot, error) {
	path := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SnapshotDiff describes the difference between two snapshots.
type SnapshotDiff struct {
	Field   string
	Message string
}

// CompareSnapshots checks that two snapshots match. Returns nil if identical.
func CompareSnapshots(want, got *Snapshot) []SnapshotDiff {
	var diffs []SnapshotDiff
	if want.Name != got.Name {
		diffs = append(diffs, SnapshotDiff{"name", "name mismatch: want " + want.Name + ", got " + got.Name})
	}
	if len(want.ToolCalls) != len(got.ToolCalls) {
		diffs = append(diffs, SnapshotDiff{"tool_calls", "call count mismatch: want " +
			string(rune(len(want.ToolCalls)+'0')) + ", got " + string(rune(len(got.ToolCalls)+'0'))})
		return diffs
	}
	for i := range want.ToolCalls {
		w, g := want.ToolCalls[i], got.ToolCalls[i]
		if w.Name != g.Name {
			diffs = append(diffs, SnapshotDiff{
				"tool_calls[" + string(rune(i+'0')) + "].name",
				"tool name mismatch: want " + w.Name + ", got " + g.Name,
			})
		}
		if w.Result.Text != g.Result.Text {
			diffs = append(diffs, SnapshotDiff{
				"tool_calls[" + string(rune(i+'0')) + "].result.text",
				"result text mismatch",
			})
		}
		if w.Result.IsError != g.Result.IsError {
			diffs = append(diffs, SnapshotDiff{
				"tool_calls[" + string(rune(i+'0')) + "].result.is_error",
				"isError mismatch: want false, got true",
			})
		}
	}
	return diffs
}

// AssertSnapshot compares got against the golden file. If the file doesn't
// exist and SNAPSHOT_UPDATE=1, it creates it. Fails the test on mismatch.
func AssertSnapshot(t *testing.T, dir, name string, got *Snapshot) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create snapshot dir: %v", err)
	}

	path := filepath.Join(dir, name+".json")
	_, statErr := os.Stat(path)

	if os.IsNotExist(statErr) || os.Getenv("SNAPSHOT_UPDATE") == "1" {
		data, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		t.Logf("snapshot written: %s", path)
		return
	}

	want, err := LoadSnapshot(dir, name)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	diffs := CompareSnapshots(want, got)
	if len(diffs) > 0 {
		for _, d := range diffs {
			t.Errorf("snapshot diff [%s]: %s", d.Field, d.Message)
		}
		t.Errorf("run SNAPSHOT_UPDATE=1 to update golden file")
	}
}
