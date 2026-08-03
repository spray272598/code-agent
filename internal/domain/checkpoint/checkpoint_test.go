package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	st := NewMemoryStore()
	s := &Snapshot{
		SessionID: "s1", Status: StatusInterrupt, Goal: "edit file",
		Pending: &PendingTool{ID: "p1", SessionID: "s1", Tool: "write_file", Reason: "write"},
	}
	if err := st.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(context.Background(), "s1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %#v", err, got)
	}
	if got.Status != StatusInterrupt || got.Pending == nil || got.Pending.Tool != "write_file" {
		t.Fatalf("bad snapshot: %+v", got)
	}
	list, _ := st.List(context.Background(), StatusInterrupt, 10)
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ck")
	st, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Snapshot{SessionID: "sess-a", Status: StatusCancelled, LastInput: "hi", CreatedAt: time.Now()}
	if err := st.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(context.Background(), "sess-a")
	if err != nil || got == nil || got.Status != StatusCancelled {
		t.Fatalf("get: %v %+v", err, got)
	}
	if err := st.Delete(context.Background(), "sess-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sess-a.json")); !os.IsNotExist(err) {
		t.Fatalf("expected deleted, err=%v", err)
	}
}

func TestRunRegistryCancel(t *testing.T) {
	reg := NewRunRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	reg.Register("s1", cancel)
	if !reg.IsRunning("s1") {
		t.Fatal("expected running")
	}
	if !reg.Cancel("s1") {
		t.Fatal("cancel false")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("ctx not cancelled")
	}
	if reg.IsRunning("s1") {
		t.Fatal("still running")
	}
}
