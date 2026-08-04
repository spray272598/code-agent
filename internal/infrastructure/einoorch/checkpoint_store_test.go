package einoorch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryCheckPointStore(t *testing.T) {
	s := NewMemoryCheckPointStore()
	ctx := context.Background()
	_, ok, err := s.Get(ctx, "missing")
	if err != nil || ok {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}
	if err := s.Set(ctx, "a", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.Get(ctx, "a")
	if err != nil || !ok || string(b) != "hello" {
		t.Fatalf("got %q ok=%v err=%v", b, ok, err)
	}
}

func TestFileCheckPointStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ck")
	s, err := NewFileCheckPointStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := "eino-sess/with:weird"
	if err := s.Set(ctx, id, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.Get(ctx, id)
	if err != nil || !ok || len(b) != 3 {
		t.Fatalf("got %v ok=%v err=%v", b, ok, err)
	}
	// ensure file exists under dir
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("no files written")
	}
}

func TestExtractFirstInterruptID_nil(t *testing.T) {
	if ExtractFirstInterruptID(nil) != "" {
		t.Fatal()
	}
}

func TestDefaultGraphCheckPointID(t *testing.T) {
	if DefaultGraphCheckPointID("s1") != "eino-s1" {
		t.Fatal(DefaultGraphCheckPointID("s1"))
	}
	if DefaultGraphCheckPointID("") != "eino-anon" {
		t.Fatal()
	}
}

func TestSafeFileID(t *testing.T) {
	if safeFileID("a/b:c") == "a/b:c" {
		t.Fatal("should sanitize")
	}
	if safeFileID("") != "empty" {
		t.Fatal()
	}
}
