package spec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSpec(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "spec.md", `---
id: my-feature
title: My Feature Implementation
goal: Implement the new dashboard feature
constraints:
  - Must use existing UI component library
  - API responses must be < 200ms p99
acceptance:
  - Dashboard loads within 2 seconds
  - All widgets display correct data
tech_notes: Use Redis caching for dashboard data
---

# My Feature Implementation

## Constraints
- Must use existing UI component library
- API responses must be < 200ms p99

## Acceptance Criteria
- Dashboard loads within 2 seconds
- All widgets display correct data
`)
	l := NewLoader(dir)
	spec := l.loadSpec()
	if spec == nil {
		t.Fatal("expected spec")
	}
	if spec.ID != "my-feature" {
		t.Fatalf("id=%s", spec.ID)
	}
	if len(spec.Constraints) == 0 {
		t.Fatal("expected constraints")
	}
	if len(spec.Acceptance) == 0 {
		t.Fatal("expected acceptance")
	}
}

func TestLoadTasks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tasks.md", `---
status: auto
---

# Tasks

- [ ] task-1: Set up API endpoints
- [x] task-2: Create database migration
- [→] task-3: Write integration tests
- [ ] task-4: Deploy to staging
`)
	l := NewLoader(dir)
	tasks := l.loadTasks()
	if len(tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(tasks))
	}
	if tasks[1].Status != "done" {
		t.Fatalf("task-2 should be done, got %s", tasks[1].Status)
	}
	if tasks[2].Status != "in_progress" {
		t.Fatalf("task-3 should be in_progress, got %s", tasks[2].Status)
	}
}

func TestLoadChecklist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "checklist.md", `# Acceptance Checklist

- [ ] Dashboard loads within 2 seconds
- [x] API returns correct data
- [x] Widgets render without errors
`)
	l := NewLoader(dir)
	items := l.loadChecklist()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[1].Status != "done" {
		t.Fatalf("item 1 should be done")
	}
}

func TestSpecBundlePrompt(t *testing.T) {
	bundle := NewSpecBundle("/tmp")
	bundle.Spec = &Spec{Title: "Test", Goal: "Do something"}
	bundle.Tasks = []Task{
		{ID: "t1", Title: "Step 1", Status: "done"},
		{ID: "t2", Title: "Step 2", Status: "pending"},
	}
	bundle.Checklist = []ChecklistItem{
		{Text: "Works correctly", Status: "pending"},
	}
	sec := bundle.PromptSection()
	if sec == "" {
		t.Fatal("expected prompt section")
	}
	if !containsStr(sec, "Test") {
		t.Fatal("expected title")
	}
	if !containsStr(sec, "[x]") {
		t.Fatal("expected done marker")
	}
	if !containsStr(sec, "[ ]") {
		t.Fatal("expected pending marker")
	}
}

func TestTrackerMarkDone(t *testing.T) {
	dir := t.TempDir()
	bundle := NewSpecBundle(dir)
	bundle.Tasks = []Task{
		{ID: "t1", Title: "Step 1", Status: "pending"},
	}
	tracker := NewTracker(dir)
	tracker.SetBundle(bundle)
	if err := tracker.MarkTaskDone("t1"); err != nil {
		t.Fatal(err)
	}
	if bundle.Tasks[0].Status != "done" {
		t.Fatal("should be done")
	}
}

func TestServiceSummary(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	summary := svc.Summary()
	if summary == "" {
		t.Fatal("expected summary")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
