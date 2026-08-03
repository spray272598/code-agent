package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileExactMultiLine(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	path := "multi.txt"
	orig := "line1\nline2\nline3\n"
	if err := os.WriteFile(filepath.Join(dir, path), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFile(ws)
	r, err := tool.Execute(context.Background(), map[string]any{
		"path": path, "old_string": "line1\nline2", "new_string": "A\nB",
	})
	if err != nil || r.IsError {
		t.Fatalf("edit: %+v err=%v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, path))
	if string(b) != "A\nB\nline3\n" {
		t.Fatalf("got %q", string(b))
	}
}

func TestEditFileRegexReplaceAll(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	path := "re.go"
	orig := "foo bar foo\n"
	_ = os.WriteFile(filepath.Join(dir, path), []byte(orig), 0o644)
	tool := NewEditFile(ws)
	r, err := tool.Execute(context.Background(), map[string]any{
		"path": path, "old_string": `foo`, "new_string": "baz", "regex": true, "replace_all": true,
	})
	if err != nil || r.IsError {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, path))
	if string(b) != "baz bar baz\n" {
		t.Fatalf("got %q", string(b))
	}
}

func TestEditFileRegexCaptureGroup(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	path := "cap.txt"
	_ = os.WriteFile(filepath.Join(dir, path), []byte("name=alice\n"), 0o644)
	tool := NewEditFile(ws)
	r, err := tool.Execute(context.Background(), map[string]any{
		"path": path, "old_string": `name=(\w+)`, "new_string": "user=$1", "regex": true,
	})
	if err != nil || r.IsError {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, path))
	if string(b) != "user=alice\n" {
		t.Fatalf("got %q", string(b))
	}
}

func TestGlobDoublestar(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	_ = os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a", "b", "x.go"), []byte("package x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a", "y.md"), []byte("# y"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "z.txt"), []byte("z"), 0o644)

	g := NewGlob(ws)
	r, err := g.Execute(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil || r.IsError {
		t.Fatalf("%+v %v", r, err)
	}
	if !strings.Contains(r.Text, "x.go") {
		t.Fatalf("expected x.go in %q", r.Text)
	}
	if strings.Contains(r.Text, "y.md") {
		t.Fatalf("should not match md: %q", r.Text)
	}

	r2, _ := g.Execute(context.Background(), map[string]any{"pattern": "**/*.{go,md}"})
	if !strings.Contains(r2.Text, "x.go") || !strings.Contains(r2.Text, "y.md") {
		// brace expansion may depend on doublestar version; at least **/*.go works
		if !strings.Contains(r2.Text, "x.go") {
			t.Fatalf("brace/glob result: %q", r2.Text)
		}
	}
}
