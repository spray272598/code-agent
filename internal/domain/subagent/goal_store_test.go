package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoalStoreSaveAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewGoalStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := &GoalSnapshot{
		ID:        "goal-1",
		Objective: "implement feature X",
		Status:    GoalStatusActive,
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}

	got, ok := store.Get("goal-1")
	if !ok || got.ID != "goal-1" {
		t.Error("expected to find goal-1")
	}
}

func TestGoalStoreList(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewGoalStore(dir)

	store.Save(&GoalSnapshot{ID: "a", Objective: "a"})
	store.Save(&GoalSnapshot{ID: "b", Objective: "b"})

	list := store.List()
	if len(list) != 2 {
		t.Errorf("expected 2 goals, got %d", len(list))
	}
}

func TestGoalStoreArchive(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewGoalStore(dir)

	store.Save(&GoalSnapshot{ID: "goal-1", Objective: "done", Status: GoalStatusComplete})
	if err := store.Archive("goal-1"); err != nil {
		t.Fatal(err)
	}

	// Should be removed from active
	_, ok := store.Get("goal-1")
	if ok {
		t.Error("archived goal should not be in active store")
	}

	// Should exist in archive directory
	archivePath := filepath.Join(dir, "archive", "goal-1.json")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("archived goal should exist on disk")
	}
}

func TestGoalStorePersistence(t *testing.T) {
	dir := t.TempDir()

	// Create store and save
	store1, _ := NewGoalStore(dir)
	store1.Save(&GoalSnapshot{ID: "persist-1", Objective: "test"})

	// Create new store from same dir — should load existing
	store2, err := NewGoalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := store2.Get("persist-1")
	if !ok || got.Objective != "test" {
		t.Error("goal should persist across store instances")
	}
}

func TestGoalStoreOnEvent(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewGoalStore(dir)

	var received []GoalEvent
	store.OnEvent(func(event GoalEvent, snapshot *GoalSnapshot) {
		received = append(received, event)
	})

	snapshot := &GoalSnapshot{ID: "e1", Objective: "test"}
	store.EmitEvent(GoalEventCreated, snapshot)
	store.EmitEvent(GoalEventGoalCompleted, snapshot)

	if len(received) != 2 || received[0] != GoalEventCreated || received[1] != GoalEventGoalCompleted {
		t.Errorf("unexpected events: %v", received)
	}
}
