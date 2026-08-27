package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRecorderBasic(t *testing.T) {
	r := NewSnapshotRecorder("basic")
	r.Record("bash", map[string]any{"command": "echo hello"}, Result{Text: "hello\n"})
	r.Record("read_file", map[string]any{"path": "/tmp/test.txt"}, Result{Text: "file contents"})

	s := r.Snapshot()
	if len(s.ToolCalls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(s.ToolCalls))
	}
	if s.ToolCalls[0].Name != "bash" {
		t.Errorf("expected bash, got %s", s.ToolCalls[0].Name)
	}
}

func TestSnapshotSaveLoad(t *testing.T) {
	dir := t.TempDir()
	r := NewSnapshotRecorder("save-load")
	r.Record("grep", map[string]any{"pattern": "TODO"}, Result{Text: "found 3 matches"})

	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSnapshot(dir, "save-load")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "save-load" {
		t.Errorf("name mismatch: %s", loaded.Name)
	}
	if len(loaded.ToolCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(loaded.ToolCalls))
	}
	if loaded.ToolCalls[0].Result.Text != "found 3 matches" {
		t.Errorf("text mismatch: %s", loaded.ToolCalls[0].Result.Text)
	}
}

func TestCompareSnapshotsIdentical(t *testing.T) {
	a := &Snapshot{Name: "test", ToolCalls: []ToolCallRecord{
		{Name: "bash", Args: map[string]any{"command": "ls"}, Result: Result{Text: "file.go"}},
	}}
	b := &Snapshot{Name: "test", ToolCalls: []ToolCallRecord{
		{Name: "bash", Args: map[string]any{"command": "ls"}, Result: Result{Text: "file.go"}},
	}}
	diffs := CompareSnapshots(a, b)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d", len(diffs))
	}
}

func TestCompareSnapshotsDifferent(t *testing.T) {
	a := &Snapshot{Name: "test", ToolCalls: []ToolCallRecord{
		{Name: "bash", Result: Result{Text: "old"}},
	}}
	b := &Snapshot{Name: "test", ToolCalls: []ToolCallRecord{
		{Name: "bash", Result: Result{Text: "new"}},
	}}
	diffs := CompareSnapshots(a, b)
	if len(diffs) == 0 {
		t.Error("expected diffs")
	}
}

func TestAssertSnapshotCreatesGolden(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "testdata", "snapshots")
	got := &Snapshot{Name: "create-golden", ToolCalls: []ToolCallRecord{
		{Name: "echo", Result: Result{Text: "ok"}},
	}}
	os.Unsetenv("SNAPSHOT_UPDATE")
	AssertSnapshot(t, dir, "create-golden", got)

	if _, err := os.Stat(filepath.Join(dir, "create-golden.json")); err != nil {
		t.Errorf("golden file not created: %v", err)
	}
}

type mockSnapshotTool struct {
	name string
}

func (m *mockSnapshotTool) Name() string                { return m.name }
func (m *mockSnapshotTool) Description() string         { return "mock tool" }
func (m *mockSnapshotTool) InputSchema() map[string]any { return nil }
func (m *mockSnapshotTool) Execute(_ context.Context, args map[string]any) (Result, error) {
	input, _ := args["input"].(string)
	return Result{Text: "echo: " + input}, nil
}

func TestSnapshotWithMockTool(t *testing.T) {
	dir := t.TempDir()
	tool := &mockSnapshotTool{name: "test_tool"}
	args := map[string]any{"input": "hello"}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	snap := &Snapshot{
		Name: "mock-tool-exec",
		ToolCalls: []ToolCallRecord{
			{Name: tool.Name(), Args: args, Result: result},
		},
	}
	AssertSnapshot(t, dir, "mock-tool-exec", snap)
}
