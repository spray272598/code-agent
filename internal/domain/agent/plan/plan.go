package plan

import (
	"fmt"
	"strings"
)

// Plan simple multi-step plan for coding tasks.
type Plan struct {
	Goal     string    `json:"goal"`
	Steps    []Step    `json:"steps"`
	Source   string    `json:"source"`
}

type Step struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Status string `json:"status"` // pending|done|failed|skipped
	Note   string `json:"note,omitempty"`
}

// BuildRulePlan heuristic breakdown for multi-step signals.
func BuildRulePlan(goal string) *Plan {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil
	}
	// only for multi-step-ish requests
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

func (p *Plan) StringForPrompt() string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Execution plan\nGoal: ")
	b.WriteString(p.Goal)
	b.WriteString("\n")
	for _, s := range p.Steps {
		b.WriteString(fmt.Sprintf("- [%s] %d. %s\n", s.Status, s.Index, s.Title))
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
		if p.Steps[i].Status == "pending" || p.Steps[i].Status == "" {
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

// Review checks remaining pending steps.
func (p *Plan) Review() (pass bool, gaps []string) {
	if p == nil || len(p.Steps) == 0 {
		return true, nil
	}
	for _, s := range p.Steps {
		if s.Status == "pending" || s.Status == "" || s.Status == "failed" {
			gaps = append(gaps, fmt.Sprintf("%d:%s(%s)", s.Index, s.Title, s.Status))
		}
	}
	return len(gaps) == 0, gaps
}
