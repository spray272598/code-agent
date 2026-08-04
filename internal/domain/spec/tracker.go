package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Tracker tracks and updates checklist and task progress.
type Tracker struct {
	mu      sync.RWMutex
	baseDir string
	bundle  *SpecBundle
}

func NewTracker(baseDir string) *Tracker {
	return &Tracker{baseDir: baseDir}
}

// SetBundle sets the current spec bundle for tracking.
func (t *Tracker) SetBundle(b *SpecBundle) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bundle = b
}

// MarkTaskDone marks a task as done by ID.
func (t *Tracker) MarkTaskDone(taskID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bundle == nil {
		return fmt.Errorf("no spec bundle loaded")
	}
	for i := range t.bundle.Tasks {
		if t.bundle.Tasks[i].ID == taskID {
			t.bundle.Tasks[i].Status = "done"
			return t.persistTasks()
		}
	}
	return fmt.Errorf("task %s not found", taskID)
}

// MarkChecklistDone marks a checklist item as done by matching text prefix.
func (t *Tracker) MarkChecklistDone(textPrefix string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bundle == nil {
		return fmt.Errorf("no spec bundle loaded")
	}
	for i := range t.bundle.Checklist {
		if strings.HasPrefix(strings.ToLower(t.bundle.Checklist[i].Text), strings.ToLower(textPrefix)) {
			t.bundle.Checklist[i].Status = "done"
			return t.persistChecklist()
		}
	}
	return fmt.Errorf("checklist item matching %q not found", textPrefix)
}

// MarkChecklistFailed marks a checklist item as failed.
func (t *Tracker) MarkChecklistFailed(textPrefix, reason string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bundle == nil {
		return fmt.Errorf("no spec bundle loaded")
	}
	for i := range t.bundle.Checklist {
		if strings.HasPrefix(strings.ToLower(t.bundle.Checklist[i].Text), strings.ToLower(textPrefix)) {
			t.bundle.Checklist[i].Status = "failed"
			if reason != "" {
				t.bundle.Checklist[i].Text += " (failed: " + reason + ")"
			}
			return t.persistChecklist()
		}
	}
	return fmt.Errorf("checklist item matching %q not found", textPrefix)
}

// CompleteProgress returns overall completion percentage.
func (t *Tracker) CompleteProgress() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.bundle == nil {
		return 0
	}
	total := len(t.bundle.Tasks) + len(t.bundle.Checklist)
	if total == 0 {
		return 0
	}
	done := 0
	for _, task := range t.bundle.Tasks {
		if task.Status == "done" {
			done++
		}
	}
	for _, item := range t.bundle.Checklist {
		if item.Status == "done" {
			done++
		}
	}
	return float64(done) / float64(total) * 100
}

// RemainingTasks returns tasks not yet done.
func (t *Tracker) RemainingTasks() []Task {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.bundle == nil {
		return nil
	}
	var remaining []Task
	for _, task := range t.bundle.Tasks {
		if task.Status != "done" {
			remaining = append(remaining, task)
		}
	}
	return remaining
}

// RemainingChecklist returns checklist items not yet done.
func (t *Tracker) RemainingChecklist() []ChecklistItem {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.bundle == nil {
		return nil
	}
	var remaining []ChecklistItem
	for _, item := range t.bundle.Checklist {
		if item.Status != "done" {
			remaining = append(remaining, item)
		}
	}
	return remaining
}

func (t *Tracker) persistTasks() error {
	path := filepath.Join(t.baseDir, "tasks.md")
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("status: auto-tracked\n")
	sb.WriteString("---\n\n")
	sb.WriteString("# Tasks\n\n")
	for _, task := range t.bundle.Tasks {
		marker := "[ ]"
		switch task.Status {
		case "done":
			marker = "[x]"
		case "in_progress":
			marker = "[→]"
		case "blocked":
			marker = "[!]"
		}
		sb.WriteString(fmt.Sprintf("- %s **%s**: %s\n", marker, task.ID, task.Title))
		if task.Description != "" {
			sb.WriteString("  > " + task.Description + "\n")
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (t *Tracker) persistChecklist() error {
	path := filepath.Join(t.baseDir, "checklist.md")
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("status: auto-tracked\n")
	sb.WriteString("---\n\n")
	sb.WriteString("# Acceptance Checklist\n\n")
	for _, item := range t.bundle.Checklist {
		marker := "[ ]"
		switch item.Status {
		case "done":
			marker = "[x]"
		case "failed":
			marker = "[✗]"
		}
		sb.WriteString(fmt.Sprintf("- %s %s\n", marker, item.Text))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
