package coding

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type stubSearcher struct {
	results []WebResult
	err     error
}

func (s *stubSearcher) Search(ctx context.Context, q string, max int) ([]WebResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	if max > len(s.results) {
		max = len(s.results)
	}
	return s.results[:max], nil
}

func TestApplyPatchNewFile(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	applyTool := NewApplyPatch(ws)
	patch := `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`
	ctx := tool.WithSessionID(context.Background(), "s1")
	res, err := applyTool.Execute(ctx, map[string]any{"patch": patch})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("apply_patch error: %s", res.Text)
	}
	got := readFile(t, dir+"/new.txt")
	if got != "hello\nworld\n" {
		t.Fatalf("content=%q", got)
	}
}

func TestApplyPatchEdit(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	writeFile(t, dir+"/f.txt", "line1\nline2\nline3\n")
	applyTool := NewApplyPatch(ws)
	patch := `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,3 +1,3 @@
 line1
-line2
+line2-changed
 line3
`
	ctx := tool.WithSessionID(context.Background(), "s1")
	res, _ := applyTool.Execute(ctx, map[string]any{"patch": patch})
	if res.IsError {
		t.Fatalf("apply_patch: %s", res.Text)
	}
	got := readFile(t, dir+"/f.txt")
	if !strings.Contains(got, "line2-changed") {
		t.Fatalf("content=%q", got)
	}
}

func TestApplyPatchRejectsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws := NewWorkspace(dir)
	applyTool := NewApplyPatch(ws)
	patch := `diff --git a/../escape.txt b/../escape.txt
--- a/../escape.txt
+++ b/../escape.txt
@@ -0,0 +1,1 @@
+evil
`
	ctx := tool.WithSessionID(context.Background(), "s1")
	res, _ := applyTool.Execute(ctx, map[string]any{"patch": patch})
	if !res.IsError {
		t.Fatal("expected sandbox rejection for parent path")
	}
}

func TestWebSearch(t *testing.T) {
	ws := NewWorkspace(t.TempDir())
	wsTool := NewWebSearch(ws, &stubSearcher{results: []WebResult{
		{Title: "Grok", URL: "https://x.ai", Snippet: "build"},
	}})
	ctx := tool.WithSessionID(context.Background(), "s1")
	res, err := wsTool.Execute(ctx, map[string]any{"query": "grok build"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "https://x.ai") {
		t.Fatalf("missing url: %s", res.Text)
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	ws := NewWorkspace(t.TempDir())
	wsTool := NewWebSearch(ws, &stubSearcher{})
	ctx := tool.WithSessionID(context.Background(), "s1")
	res, _ := wsTool.Execute(ctx, map[string]any{"query": ""})
	if !res.IsError {
		t.Fatal("expected error on empty query")
	}
}

func TestStripTags(t *testing.T) {
	in := `<a href="x">Hello &amp; World</a>`
	if got := stripTags(in); got != "Hello & World" {
		t.Fatalf("got %q", got)
	}
}
