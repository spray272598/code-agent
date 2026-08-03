package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildAndSearch(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "alpha.go"), []byte("package main\nfunc CheckpointSave() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# demo\ncode search retriever\n"), 0o644)
	idx := New(dir)
	st, err := idx.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Files < 2 {
		t.Fatalf("files=%d", st.Files)
	}
	hits := idx.Search("CheckpointSave", 5)
	if len(hits) == 0 {
		t.Fatal("expected hit")
	}
	if hits[0].Path != "alpha.go" {
		t.Fatalf("path=%s", hits[0].Path)
	}
	// tool path
	tr := NewSearchTool(idx)
	res, err := tr.Execute(context.Background(), map[string]any{"query": "retriever"})
	if err != nil || res.IsError {
		t.Fatalf("%v %#v", err, res)
	}
}
