package application

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/domain/hook"
)

// newTestApp builds a ChatApp with only checkpoint + hooks wired (for
// touchStep / ListResumable testing without a full engine).
func newTestApp(store checkpoint.Store, runs *checkpoint.RunRegistry) *ChatApp {
	a := New(CoreDeps{})
	a.ckStore = store
	a.runs = runs
	a.hooks = hook.NewBus()
	return a
}

func TestTouchStepOnlyRunning(t *testing.T) {
	store := checkpoint.NewMemoryStore()
	runs := checkpoint.NewRunRegistry()
	a := newTestApp(store, runs)

	ctx := context.Background()
	// running snapshot → step updated
	_ = store.Save(ctx, &checkpoint.Snapshot{SessionID: "s1", Status: checkpoint.StatusRunning})
	a.touchStep(ctx, "s1", 3, "fs_read")
	snap, _ := store.Get(ctx, "s1")
	if snap.Step != 3 {
		t.Fatalf("expected step=3, got %d", snap.Step)
	}
	if snap.Meta["lastTool"] != "fs_read" {
		t.Fatalf("expected lastTool=fs_read, got %v", snap.Meta["lastTool"])
	}

	// non-running snapshot → ignored
	_ = store.Save(ctx, &checkpoint.Snapshot{SessionID: "s2", Status: checkpoint.StatusCompleted})
	a.touchStep(ctx, "s2", 5, "bash")
	snap2, _ := store.Get(ctx, "s2")
	if snap2.Step != 0 {
		t.Fatalf("completed snapshot should not be touched, got step=%d", snap2.Step)
	}
}

func TestListResumableFiltersActive(t *testing.T) {
	store := checkpoint.NewMemoryStore()
	runs := checkpoint.NewRunRegistry()
	a := newTestApp(store, runs)
	ctx := context.Background()

	// orphan running snapshot (no active handle)
	_ = store.Save(ctx, &checkpoint.Snapshot{SessionID: "orphan", Status: checkpoint.StatusRunning, Step: 2})
	// actively-running snapshot
	_ = store.Save(ctx, &checkpoint.Snapshot{SessionID: "active", Status: checkpoint.StatusRunning, Step: 1})
	runs.Register("active", func() {})
	defer runs.Unregister("active", func() {})

	list := a.ListResumable(ctx)
	if len(list) != 1 || list[0].SessionID != "orphan" {
		t.Fatalf("expected only orphan resumable, got %#v", list)
	}
}

func TestQuotaExceeded(t *testing.T) {
	// Unlimited when quota<=0.
	if quotaExceeded(999999, 0) {
		t.Fatal("quota<=0 must never be exceeded")
	}
	// Under limit.
	if quotaExceeded(100, 200) {
		t.Fatal("used=100 should be under quota=200")
	}
	// At limit is exceeded (prevent the request that would push past).
	if !quotaExceeded(200, 200) {
		t.Fatal("used==quota should be exceeded")
	}
	if !quotaExceeded(201, 200) {
		t.Fatal("used>quota should be exceeded")
	}
}
