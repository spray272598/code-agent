package blob_test

import (
	"context"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/infrastructure/storage"
)

func TestOffloadIfLarge(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("hello world ", 1000)
	or := blob.OffloadIfLarge(context.Background(), st, "sess1", "bash", big, 100)
	if !or.Offloaded {
		t.Fatal("expected offload")
	}
	if or.ObjectKey == "" {
		t.Fatal("missing key")
	}
	data, err := st.Get(context.Background(), or.ObjectKey)
	if err != nil || len(data) != len(big) {
		t.Fatalf("get: %v len=%d", err, len(data))
	}
	small := blob.OffloadIfLarge(context.Background(), st, "sess1", "bash", "tiny", 100)
	if small.Offloaded {
		t.Fatal("should not offload tiny")
	}
}
