package spec

import (
	"fmt"
	"log"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/agent/plan"
)

// Service is the facade for the spec system.
type Service struct {
	mu      sync.RWMutex
	loader  *Loader
	tracker *Tracker
	bundle  *SpecBundle
}

func NewService(baseDir string) *Service {
	s := &Service{
		loader:  NewLoader(baseDir),
		tracker: NewTracker(baseDir),
	}
	if err := s.Reload(); err != nil {
		log.Printf("[spec] reload: %v", err)
	}
	return s
}

// Reload reloads all spec files from disk.
func (s *Service) Reload() error {
	bundle, err := s.loader.LoadAll()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.bundle = bundle
	s.tracker.SetBundle(bundle)
	s.mu.Unlock()
	return nil
}

// Bundle returns the current spec bundle.
func (s *Service) Bundle() *SpecBundle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bundle
}

// HasSpec reports whether a spec.md exists.
func (s *Service) HasSpec() bool {
	return s.loader.HasSpec()
}

// HasCLAUDE reports whether a CLAUDE.md exists.
func (s *Service) HasCLAUDE() bool {
	return s.loader.HasCLAUDE()
}

// PromptSection returns the formatted spec content for system prompt injection.
func (s *Service) PromptSection() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle == nil {
		return ""
	}
	return s.bundle.PromptSection()
}

// MarkTaskDone marks a task done by ID.
func (s *Service) MarkTaskDone(taskID string) error {
	err := s.tracker.MarkTaskDone(taskID)
	if err == nil {
		log.Printf("[spec] task %s marked done", taskID)
	}
	return err
}

// MarkChecklistDone marks a checklist item done.
func (s *Service) MarkChecklistDone(textPrefix string) error {
	return s.tracker.MarkChecklistDone(textPrefix)
}

// MarkChecklistFailed marks a checklist item failed.
func (s *Service) MarkChecklistFailed(textPrefix, reason string) error {
	return s.tracker.MarkChecklistFailed(textPrefix, reason)
}

// Progress returns overall completion percentage.
func (s *Service) Progress() float64 {
	return s.tracker.CompleteProgress()
}

// RemainingTasks returns pending tasks.
func (s *Service) RemainingTasks() []Task {
	return s.tracker.RemainingTasks()
}

// --- plan.SpecData interface implementation ---

// GetTitle implements plan.SpecData.
func (s *Service) GetTitle() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle != nil && s.bundle.Spec != nil {
		return s.bundle.Spec.Title
	}
	return ""
}

// GetGoal implements plan.SpecData.
func (s *Service) GetGoal() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle != nil && s.bundle.Spec != nil {
		return s.bundle.Spec.Goal
	}
	return ""
}

// GetTasks implements plan.SpecData.
func (s *Service) GetTasks() []plan.TaskData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle == nil {
		return nil
	}
	var out []plan.TaskData
	for _, t := range s.bundle.Tasks {
		out = append(out, plan.TaskData{ID: t.ID, Title: t.Title, Status: t.Status})
	}
	return out
}

// GetChecklist implements plan.SpecData.
func (s *Service) GetChecklist() []plan.ChecklistData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle == nil {
		return nil
	}
	var out []plan.ChecklistData
	for _, c := range s.bundle.Checklist {
		out = append(out, plan.ChecklistData{Text: c.Text, Status: c.Status})
	}
	return out
}

// GetConstraints implements plan.SpecData.
func (s *Service) GetConstraints() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle != nil && s.bundle.Spec != nil {
		return s.bundle.Spec.Constraints
	}
	return nil
}

// GetAcceptance implements plan.SpecData.
func (s *Service) GetAcceptance() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle != nil && s.bundle.Spec != nil {
		return s.bundle.Spec.Acceptance
	}
	return nil
}

// HasContent implements plan.SpecData.
func (s *Service) HasContent() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bundle == nil {
		return false
	}
	return !s.bundle.IsEmpty()
}

// Ensure Service satisfies plan.SpecData.
var _ plan.SpecData = (*Service)(nil)

// Summary returns a human-readable summary of the current status.
func (s *Service) Summary() string {
	b := s.Bundle()
	if b == nil || b.IsEmpty() {
		return "No spec loaded. Create spec.md in the project root to enable spec-driven development."
	}
	var msg string
	if b.Spec != nil {
		msg += fmt.Sprintf("Spec: %s\n", b.Spec.Title)
	}
	if len(b.Tasks) > 0 {
		done, total := 0, len(b.Tasks)
		for _, t := range b.Tasks {
			if t.Status == "done" {
				done++
			}
		}
		msg += fmt.Sprintf("Tasks: %d/%d done\n", done, total)
	}
	if len(b.Checklist) > 0 {
		done, total := 0, len(b.Checklist)
		for _, c := range b.Checklist {
			if c.Status == "done" {
				done++
			}
		}
		msg += fmt.Sprintf("Checklist: %d/%d done\n", done, total)
	}
	msg += fmt.Sprintf("Progress: %.0f%%", s.Progress())
	return msg
}
