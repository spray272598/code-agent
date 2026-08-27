package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

func normalizeSnapshot(s *tool.Snapshot, tmpDir string) *tool.Snapshot {
	cp := *s
	cp.ToolCalls = make([]tool.ToolCallRecord, len(s.ToolCalls))
	for i, tc := range s.ToolCalls {
		cp.ToolCalls[i] = tc
		cp.ToolCalls[i].Result.Text = strings.ReplaceAll(tc.Result.Text, tmpDir, "<TEMP>")
	}
	return &cp
}

func TestSnapshotReadFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "hello.txt")
	os.WriteFile(fp, []byte("hello world"), 0644)

	ws := NewWorkspace(dir)
	readTool := &ReadFileTool{ws: ws}
	result, err := readTool.Execute(context.Background(), map[string]any{"path": fp})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", result.Text)
	}

	snap := normalizeSnapshot(&tool.Snapshot{
		Name: "read-file-basic",
		ToolCalls: []tool.ToolCallRecord{
			{Name: "read_file", Args: map[string]any{"path": fp}, Result: result},
		},
	}, dir)
	tool.AssertSnapshot(t, filepath.Join("testdata", "snapshots"), "read-file-basic", snap)
}

func TestSnapshotWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "output.txt")

	ws := NewWorkspace(dir)
	writeTool := &WriteFileTool{ws: ws}
	readTool := &ReadFileTool{ws: ws}

	wr, _ := writeTool.Execute(context.Background(), map[string]any{"path": fp, "content": "written by test"})
	rr, _ := readTool.Execute(context.Background(), map[string]any{"path": fp})

	snap := normalizeSnapshot(&tool.Snapshot{
		Name: "write-then-read",
		ToolCalls: []tool.ToolCallRecord{
			{Name: "write_file", Args: map[string]any{"path": fp, "content": "written by test"}, Result: wr},
			{Name: "read_file", Args: map[string]any{"path": fp}, Result: rr},
		},
	}, dir)
	tool.AssertSnapshot(t, filepath.Join("testdata", "snapshots"), "write-then-read", snap)
}

func TestSnapshotGlob(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "util.go"), []byte("package util"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)

	ws := NewWorkspace(dir)
	globTool := &GlobTool{ws: ws}
	result, err := globTool.Execute(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}

	snap := normalizeSnapshot(&tool.Snapshot{
		Name: "glob-go-files",
		ToolCalls: []tool.ToolCallRecord{
			{Name: "glob", Args: map[string]any{"pattern": "**/*.go"}, Result: result},
		},
	}, dir)
	tool.AssertSnapshot(t, filepath.Join("testdata", "snapshots"), "glob-go-files", snap)
}

func TestGoldenFilesValid(t *testing.T) {
	snapDir := filepath.Join("testdata", "snapshots")
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		t.Skip("no snapshot directory")
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(snapDir, e.Name()))
		if err != nil {
			t.Errorf("read %s: %v", e.Name(), err)
			continue
		}
		var s tool.Snapshot
		if err := json.Unmarshal(data, &s); err != nil {
			t.Errorf("parse %s: %v", e.Name(), err)
		}
	}
}
