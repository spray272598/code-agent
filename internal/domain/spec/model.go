package spec

import (
	"fmt"
	"strings"
)

// Spec represents a spec.md parsed document.
type Spec struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Goal        string            `json:"goal"`
	Constraints []string          `json:"constraints,omitempty"`
	Acceptance  []string          `json:"acceptance,omitempty"`
	TechNotes   string            `json:"tech_notes,omitempty"`
	Body        string            `json:"-"`
	Meta        map[string]string `json:"meta,omitempty"`
	LoadedAt    string            `json:"loaded_at,omitempty"`
}

// Task represents a tasks.md task item.
type Task struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Depends     []string `json:"depends,omitempty"`
	Status      string   `json:"status"` // pending|in_progress|done|blocked
	Note        string   `json:"note,omitempty"`
}

// ChecklistItem represents a checklist.md item.
type ChecklistItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"` // pending|done|failed
	Group  string `json:"group,omitempty"`
}

// SpecBundle holds the complete spec + tasks + checklist set.
type SpecBundle struct {
	Spec      *Spec           `json:"spec"`
	Tasks     []Task          `json:"tasks"`
	Checklist []ChecklistItem `json:"checklist"`
	ClaudeMD  string          `json:"claude_md,omitempty"`
	BaseDir   string          `json:"base_dir"`
}

// NewSpecBundle creates an empty bundle.
func NewSpecBundle(baseDir string) *SpecBundle {
	return &SpecBundle{
		BaseDir: baseDir,
		Tasks:   []Task{},
	}
}

// IsEmpty reports whether the bundle has meaningful content.
func (b *SpecBundle) IsEmpty() bool {
	return b.Spec == nil && len(b.Tasks) == 0 && len(b.Checklist) == 0 && b.ClaudeMD == ""
}

// PromptSection formats the bundle for system prompt injection.
func (b *SpecBundle) PromptSection() string {
	if b == nil || b.IsEmpty() {
		return ""
	}
	var sb []string
	if b.ClaudeMD != "" {
		sb = append(sb, "## Project Rules (CLAUDE.md)\n"+b.ClaudeMD)
	}
	if b.Spec != nil {
		s := b.Spec
		sec := "## Active Spec\n"
		sec += "Title: " + s.Title + "\n"
		sec += "Goal: " + s.Goal + "\n"
		if len(s.Constraints) > 0 {
			sec += "Constraints:\n"
			for _, c := range s.Constraints {
				sec += "- " + c + "\n"
			}
		}
		if len(s.Acceptance) > 0 {
			sec += "Acceptance criteria:\n"
			for _, a := range s.Acceptance {
				sec += "- " + a + "\n"
			}
		}
		if s.TechNotes != "" {
			sec += "Technical notes: " + s.TechNotes + "\n"
		}
		sb = append(sb, sec)
	}
	if len(b.Tasks) > 0 {
		sec := "## Task Breakdown\n"
		for _, t := range b.Tasks {
			status := "[ ]"
			if t.Status == "done" {
				status = "[x]"
			} else if t.Status == "in_progress" {
				status = "[→]"
			} else if t.Status == "blocked" {
				status = "[!]"
			}
			sec += fmt.Sprintf("- %s %s: %s\n", status, t.ID, t.Title)
		}
		sb = append(sb, sec)
	}
	if len(b.Checklist) > 0 {
		sec := "## Acceptance Checklist\n"
		for _, c := range b.Checklist {
			status := "[ ]"
			if c.Status == "done" {
				status = "[x]"
			} else if c.Status == "failed" {
				status = "[✗]"
			}
			sec += fmt.Sprintf("- %s %s\n", status, c.Text)
		}
		sb = append(sb, sec)
	}
	return strings.Join(sb, "\n\n")
}
