package plan

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Plan multi-step plan for coding tasks.
type Plan struct {
	Goal    string `json:"goal"`
	Steps   []Step `json:"steps"`
	Source  string `json:"source"` // rule|spec|llm|dynamic
	SpecRef string `json:"spec_ref,omitempty"`

	// Dynamic plan features
	mu          sync.RWMutex    `json:"-"`
	PendingSub  []SubTask       `json:"pending_sub,omitempty"` // sub-tasks to generate
	GoalState   GoalState       `json:"goal_state,omitempty"`
	Approval    *PlanApproval   `json:"approval,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type Step struct {
	Index      int      `json:"index"`
	Title      string   `json:"title"`
	Status     string   `json:"status"` // pending|done|failed|skipped|in_progress
	Note       string   `json:"note,omitempty"`
	DependsOn  []int    `json:"depends_on,omitempty"` // indices of prerequisite steps
	CanRunInParallel bool `json:"can_run_in_parallel,omitempty"`
	SubSteps   []Step   `json:"sub_steps,omitempty"` // dynamic sub-steps
}

// SubTask represents a task that can be dynamically expanded into sub-steps.
type SubTask struct {
	ParentIndex int    `json:"parent_index"`
	Description string `json:"description"`
	Reason      string `json:"reason"` // why this needs expansion
}

// GoalState tracks the lifecycle of a plan goal.
type GoalState string

const (
	GoalActive   GoalState = "active"
	GoalPaused   GoalState = "paused"
	GoalBlocked  GoalState = "blocked"
	GoalComplete GoalState = "complete"
)

// GoalBlockReason explains why a goal is blocked.
type GoalBlockReason struct {
	Code    string `json:"code"`    // machine-readable: "dependency", "error", "user_input"
	Message string `json:"message"` // human-readable explanation
}

// PlanApproval tracks whether the plan needs user approval before execution.
type PlanApproval struct {
	Required bool      `json:"required"`
	Status   string    `json:"status"` // pending|approved|rejected
	Reason   string    `json:"reason,omitempty"`
	At       time.Time `json:"at,omitempty"`
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

	analysis := analyzeComplexity(goal)
	now := time.Now()

	if !analysis.isComplex {
		return &Plan{
			Goal:      goal,
			Source:    "rule",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []Step{
				{Index: 1, Title: "Understand task and gather context", Status: "pending"},
				{Index: 2, Title: "Implement solution", Status: "pending"},
				{Index: 3, Title: "Verify and summarize", Status: "pending"},
			},
		}
	}

	steps := generateDynamicSteps(goal, analysis)

	return &Plan{
		Goal:      goal,
		Source:    "rule",
		CreatedAt: now,
		UpdatedAt: now,
		Steps:     steps,
	}
}

type complexityAnalysis struct {
	isComplex bool
	hasSearch bool
	hasEdit   bool
	hasTest   bool
	hasDeploy bool
	keywords  []string
}

func analyzeComplexity(goal string) complexityAnalysis {
	lower := strings.ToLower(goal)
	a := complexityAnalysis{}

	signals := []string{"然后", "接着", "并且", "and then", "after", "先", "再", "完整", "step", "实现", "开发", "创建", "build", "implement", "create", "develop"}
	for _, s := range signals {
		if strings.Contains(lower, s) || strings.Contains(goal, s) {
			a.isComplex = true
			a.keywords = append(a.keywords, s)
		}
	}

	if len([]rune(goal)) > 60 {
		a.isComplex = true
	}

	searchSignals := []string{"搜索", "查找", "找到", "search", "find", "locate", "grep"}
	for _, s := range searchSignals {
		if strings.Contains(lower, s) {
			a.hasSearch = true
			break
		}
	}

	editSignals := []string{"修改", "编辑", "更新", "添加", "创建", "实现", "fix", "change", "edit", "update", "add", "implement"}
	for _, s := range editSignals {
		if strings.Contains(lower, s) {
			a.hasEdit = true
			break
		}
	}

	testSignals := []string{"测试", "验证", "test", "verify", "check", "确保"}
	for _, s := range testSignals {
		if strings.Contains(lower, s) {
			a.hasTest = true
			break
		}
	}

	deploySignals := []string{"部署", "推送", "发布", "deploy", "push", "release", "build"}
	for _, s := range deploySignals {
		if strings.Contains(lower, s) {
			a.hasDeploy = true
			break
		}
	}

	return a
}

func generateDynamicSteps(goal string, a complexityAnalysis) []Step {
	var steps []Step
	idx := 1

	if a.hasSearch {
		steps = append(steps, Step{Index: idx, Title: "Explore codebase (glob/grep/read)", Status: "pending"})
		idx++
	}

	if a.hasEdit {
		steps = append(steps, Step{Index: idx, Title: "Plan changes and implement", Status: "pending"})
		idx++
	}

	if a.hasTest {
		steps = append(steps, Step{Index: idx, Title: "Test and verify", Status: "pending"})
		idx++
	}

	if a.hasDeploy {
		steps = append(steps, Step{Index: idx, Title: "Build and deploy", Status: "pending"})
		idx++
	}

	if len(steps) == 0 {
		steps = []Step{
			{Index: 1, Title: "Explore and understand", Status: "pending"},
			{Index: 2, Title: "Implement changes", Status: "pending"},
			{Index: 3, Title: "Verify and summarize", Status: "pending"},
		}
	}

	steps = append(steps, Step{Index: idx, Title: "Summarize results", Status: "pending"})
	return steps
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
	now := time.Now()
	plan := &Plan{
		Goal:      goal,
		Source:    "spec",
		SpecRef:   sd.GetTitle(),
		CreatedAt: now,
		UpdatedAt: now,
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
		if len(s.SubSteps) > 0 {
			for _, sub := range s.SubSteps {
				subMarker := "[ ]"
				switch sub.Status {
				case "done":
					subMarker = "[x]"
				case "in_progress":
					subMarker = "[→]"
				}
				b.WriteString(fmt.Sprintf("  %s %d.%d %s\n", subMarker, s.Index, sub.Index, sub.Title))
			}
		}
	}
	return b.String()
}

// Advance marks first pending as done (or failed).
func (p *Plan) Advance(ok bool, note string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Steps {
		if p.Steps[i].Status == "pending" || p.Steps[i].Status == "" || p.Steps[i].Status == "in_progress" {
			if ok {
				p.Steps[i].Status = "done"
			} else {
				p.Steps[i].Status = "failed"
			}
			p.Steps[i].Note = note
			p.UpdatedAt = time.Now()
			return
		}
	}
}

// AdvanceByIndex marks a specific step by index as done.
func (p *Plan) AdvanceByIndex(index int, ok bool, note string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Steps {
		if p.Steps[i].Index == index {
			if ok {
				p.Steps[i].Status = "done"
			} else {
				p.Steps[i].Status = "failed"
			}
			p.Steps[i].Note = note
			p.UpdatedAt = time.Now()
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

// PlanStepView is a single step's renderable snapshot.
type PlanStepView struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// PlanView is a structured, render-ready snapshot of a plan. UI layers can
// consume it directly to draw progress trees without re-parsing prompt text.
type PlanView struct {
	Goal     string         `json:"goal"`
	Source   string         `json:"source"`
	SpecRef  string         `json:"spec_ref,omitempty"`
	Progress float64        `json:"progress"` // 0-100
	Total    int            `json:"total"`
	Done     int            `json:"done"`
	Failed   int            `json:"failed"`
	Current  int            `json:"current"` // index of first pending/in_progress step, 0 if none
	Steps    []PlanStepView `json:"steps"`
}

// View returns a render-ready snapshot of the plan.
func (p *Plan) View() *PlanView {
	if p == nil {
		return nil
	}
	done, failed, current := 0, 0, 0
	steps := make([]PlanStepView, 0, len(p.Steps))
	for _, s := range p.Steps {
		switch s.Status {
		case "done":
			done++
		case "failed":
			failed++
		}
		if current == 0 && (s.Status == "pending" || s.Status == "" || s.Status == "in_progress") {
			current = s.Index
		}
		steps = append(steps, PlanStepView{Index: s.Index, Title: s.Title, Status: s.Status, Note: s.Note})
	}
	return &PlanView{
		Goal:     p.Goal,
		Source:   p.Source,
		SpecRef:  p.SpecRef,
		Progress: p.Progress(),
		Total:    len(p.Steps),
		Done:     done,
		Failed:   failed,
		Current:  current,
		Steps:    steps,
	}
}

// Visualize renders an ASCII tree of the plan for terminals / logs.
func (p *Plan) Visualize() string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Plan: ")
	b.WriteString(p.Goal)
	if p.SpecRef != "" {
		b.WriteString(fmt.Sprintf(" (from %s)", p.SpecRef))
	}
	b.WriteString(fmt.Sprintf("  [%.0f%%]\n", p.Progress()))
	for i, s := range p.Steps {
		branch := "├─"
		if i == len(p.Steps)-1 {
			branch = "└─"
		}
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
		b.WriteString(fmt.Sprintf("%s %s %d. %s", branch, marker, s.Index, s.Title))
		if s.Note != "" {
			b.WriteString("  (" + s.Note + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Replan rebuilds a fresh rule-driven plan from a (possibly revised) goal.
func (p *Plan) Replan(newGoal string) *Plan {
	if p == nil {
		return nil
	}
	goal := p.Goal
	if strings.TrimSpace(newGoal) != "" {
		goal = strings.TrimSpace(newGoal)
	}
	np := BuildRulePlan(goal)
	if np == nil {
		np = &Plan{Goal: goal, Source: "replan"}
	}
	np.Source = "replan"
	if p.SpecRef != "" {
		np.SpecRef = p.SpecRef
	}
	return np
}

// --- Dynamic Plan Features ---

// RequestSubTaskExpansion marks a step for dynamic sub-task generation.
func (p *Plan) RequestSubTaskExpansion(parentIndex int, description, reason string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.PendingSub = append(p.PendingSub, SubTask{
		ParentIndex: parentIndex,
		Description: description,
		Reason:      reason,
	})
	p.UpdatedAt = time.Now()
}

// ExpandStep adds sub-steps to a parent step (called after LLM generates them).
func (p *Plan) ExpandStep(parentIndex int, subSteps []Step) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Steps {
		if p.Steps[i].Index == parentIndex {
			p.Steps[i].SubSteps = append(p.Steps[i].SubSteps, subSteps...)
			// Remove from pending
			for j, sub := range p.PendingSub {
				if sub.ParentIndex == parentIndex {
					p.PendingSub = append(p.PendingSub[:j], p.PendingSub[j+1:]...)
					break
				}
			}
			p.UpdatedAt = time.Now()
			return
		}
	}
}

// HasPendingExpansions returns true if there are steps waiting for sub-task generation.
func (p *Plan) HasPendingExpansions() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.PendingSub) > 0
}

// SetGoalState updates the goal lifecycle state.
func (p *Plan) SetGoalState(state GoalState, reason *GoalBlockReason) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.GoalState = state
	p.UpdatedAt = time.Now()
}

// GetGoalState returns the current goal state.
func (p *Plan) GetGoalState() GoalState {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.GoalState
}

// RequestApproval marks the plan as needing user approval before execution.
func (p *Plan) RequestApproval(reason string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Approval = &PlanApproval{
		Required: true,
		Status:   "pending",
		Reason:   reason,
	}
	p.UpdatedAt = time.Now()
}

// Approve marks the plan as approved by the user.
func (p *Plan) Approve() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Approval != nil {
		p.Approval.Status = "approved"
		p.Approval.At = time.Now()
	}
	p.UpdatedAt = time.Now()
}

// Reject marks the plan as rejected by the user.
func (p *Plan) Reject(reason string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Approval != nil {
		p.Approval.Status = "rejected"
		p.Approval.Reason = reason
		p.Approval.At = time.Now()
	}
	p.UpdatedAt = time.Now()
}

// NeedsApproval returns true if the plan is waiting for user approval.
func (p *Plan) NeedsApproval() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Approval != nil && p.Approval.Required && p.Approval.Status == "pending"
}

// AddDependsOn adds a dependency between steps.
func (p *Plan) AddDependsOn(stepIndex, dependsOn int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Steps {
		if p.Steps[i].Index == stepIndex {
			p.Steps[i].DependsOn = append(p.Steps[i].DependsOn, dependsOn)
			p.UpdatedAt = time.Now()
			return
		}
	}
}

// GetReadySteps returns steps that are pending and have all dependencies satisfied.
func (p *Plan) GetReadySteps() []Step {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Build set of done indices
	doneSet := map[int]bool{}
	for _, s := range p.Steps {
		if s.Status == "done" {
			doneSet[s.Index] = true
		}
	}

	var ready []Step
	for _, s := range p.Steps {
		if s.Status != "pending" {
			continue
		}
		allDepsDone := true
		for _, dep := range s.DependsOn {
			if !doneSet[dep] {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			ready = append(ready, s)
		}
	}
	return ready
}
