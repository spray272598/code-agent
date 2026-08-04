package plan

import (
	"fmt"
	"strings"
)

// Plan multi-step plan for coding tasks.
type Plan struct {
	Goal     string    `json:"goal"`
	Steps    []Step    `json:"steps"`
	Source   string    `json:"source"` // rule|spec|llm
	SpecRef  string    `json:"spec_ref,omitempty"`
}

type Step struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Status string `json:"status"` // pending|done|failed|skipped
	Note   string `json:"note,omitempty"`
}

// SpecData is the minimal spec interface the plan system needs (avoids import cycle).
type SpecData interface {
	GetTitle() string
	GetGoal() string
	GetTasks() []TaskData
	GetChecklist() []ChecklistData
	GetConstraints() []string
	GetAcceptance() []string
	HasContent() bool
}

type TaskData struct {
	ID     string
	Title  string
	Status string // pending|done|in_progress|blocked
}

type ChecklistData struct {
	Text   string
	Status string // pending|done|failed
}

// BuildRulePlan heuristic breakdown for multi-step signals (fallback when no spec).
func BuildRulePlan(goal string) *Plan {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil
	}
	signals := []string{"然后", "接着", "并且", "and then", "after", "先", "再", "完整", "step"}
	hit := len([]rune(goal)) > 60
	lower := strings.ToLower(goal)
	for _, s := range signals {
		if strings.Contains(lower, s) || strings.Contains(goal, s) {
			hit = true
			break
		}
	}
	if !hit {
		return nil
	}
	return &Plan{
		Goal:   goal,
		Source: "rule",
		Steps: []Step{
			{Index: 1, Title: "Explore codebase (glob/grep/read)", Status: "pending"},
			{Index: 2, Title: "Apply changes (edit/write)", Status: "pending"},
			{Index: 3, Title: "Verify (bash/test or re-read)", Status: "pending"},
			{Index: 4, Title: "Summarize result", Status: "pending"},
		},
	}
}

// BuildFromSpec creates a plan from SpecData (spec-driven development).
func BuildFromSpec(sd SpecData) *Plan {
	if sd == nil || !sd.HasContent() {
		return nil
	}
	goal := sd.GetGoal()
	if goal == "" {
		goal = sd.GetTitle()
	}
	plan := &Plan{
		Goal:    goal,
		Source:  "spec",
		SpecRef: sd.GetTitle(),
	}

	// Build steps from spec tasks
	for i, t := range sd.GetTasks() {
		status := "pending"
		switch t.Status {
		case "done":
			status = "done"
		case "in_progress":
			status = "in_progress"
		case "blocked":
			status = "blocked"
		}
		plan.Steps = append(plan.Steps, Step{
			Index:  i + 1,
			Title:  fmt.Sprintf("[%s] %s", t.ID, t.Title),
			Status: status,
		})
	}

	// Add acceptance steps from checklist
	for _, c := range sd.GetChecklist() {
		status := "pending"
		if c.Status == "done" {
			status = "done"
		}
		plan.Steps = append(plan.Steps, Step{
			Index:  len(plan.Steps) + 1,
			Title:  fmt.Sprintf("[check] %s", c.Text),
			Status: status,
		})
	}

	return plan
}

// BuildPlan picks spec-driven or rule-driven based on available data.
func BuildPlan(userInput string, sd SpecData) *Plan {
	if sd != nil && sd.HasContent() {
		if p := BuildFromSpec(sd); p != nil {
			if userInput == "" {
				return p
			}
			// merge: spec plan + user goal
			return p
		}
	}
	return BuildRulePlan(userInput)
}

func (p *Plan) StringForPrompt() string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Execution plan\nGoal: ")
	b.WriteString(p.Goal)
	if p.SpecRef != "" {
		b.WriteString(fmt.Sprintf(" (from spec: %s)", p.SpecRef))
	}
	b.WriteString("\n")
	for _, s := range p.Steps {
		marker := "[ ]"
		switch s.Status {
		case "done":
			marker = "[x]"
		case "in_progress":
			marker = "[→]"
		case "blocked":
			marker = "[!]"
		case "failed":
			marker = "[✗]"
		}
		b.WriteString(fmt.Sprintf("%s %d. %s\n", marker, s.Index, s.Title))
		if s.Note != "" {
			b.WriteString("  note: " + s.Note + "\n")
		}
	}
	return b.String()
}

// Advance marks first pending as done (or failed).
func (p *Plan) Advance(ok bool, note string) {
	if p == nil {
		return
	}
	for i := range p.Steps {
		if p.Steps[i].Status == "pending" || p.Steps[i].Status == "" || p.Steps[i].Status == "in_progress" {
			if ok {
				p.Steps[i].Status = "done"
			} else {
				p.Steps[i].Status = "failed"
			}
			p.Steps[i].Note = note
			return
		}
	}
}

// AdvanceByIndex marks a specific step by index as done.
func (p *Plan) AdvanceByIndex(index int, ok bool, note string) {
	if p == nil {
		return
	}
	for i := range p.Steps {
		if p.Steps[i].Index == index {
			if ok {
				p.Steps[i].Status = "done"
			} else {
				p.Steps[i].Status = "failed"
			}
			p.Steps[i].Note = note
			return
		}
	}
}

// Review checks remaining pending/in_progress steps.
func (p *Plan) Review() (pass bool, gaps []string) {
	if p == nil || len(p.Steps) == 0 {
		return true, nil
	}
	for _, s := range p.Steps {
		if s.Status == "pending" || s.Status == "" || s.Status == "failed" || s.Status == "in_progress" {
			gaps = append(gaps, fmt.Sprintf("%d:%s(%s)", s.Index, s.Title, s.Status))
		}
	}
	return len(gaps) == 0, gaps
}

// Progress returns completion percentage.
func (p *Plan) Progress() float64 {
	if p == nil || len(p.Steps) == 0 {
		return 0
	}
	done := 0
	for _, s := range p.Steps {
		if s.Status == "done" {
			done++
		}
	}
	return float64(done) / float64(len(p.Steps)) * 100
}
