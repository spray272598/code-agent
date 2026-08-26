package team

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/subagent"
)

// PhaseState represents the current state of a collaboration phase.
type PhaseState string

const (
	PhasePending  PhaseState = "pending"
	PhaseRunning  PhaseState = "running"
	PhaseFeedback PhaseState = "feedback"
	PhaseComplete PhaseState = "complete"
	PhaseFailed   PhaseState = "failed"
	PhaseBlocked  PhaseState = "blocked"
)

// CollaborationState tracks the full multi-agent workflow.
type CollaborationState struct {
	ID            string
	Mode          string
	Goal          string
	Phases        []*Phase
	CurrentIdx    int
	FeedbackCount int
	MaxFeedback   int
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Phase represents one step in the collaboration workflow.
type Phase struct {
	ID           string
	Name         string
	Role         string
	State        PhaseState
	Spec         subagent.Spec
	Result       *subagent.Result
	Feedback     string
	RetryOf      string
	Dependencies []string
	StartedAt    time.Time
	CompletedAt  time.Time
}

// Collaboration orchestrates multi-agent workflows with feedback loops.
type Collaboration struct {
	mu        sync.Mutex
	state     *CollaborationState
	runner    *subagent.Runner
	llm       port.ILLMPort
	specs     []subagent.Spec
	results   map[string]*subagent.Result
	published chan<- CollaborationEvent
}

// CollaborationEvent is emitted during workflow execution.
type CollaborationEvent struct {
	Type    string      `json:"type"`
	PhaseID string      `json:"phaseId,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// NewCollaboration creates a new collaboration workflow.
func NewCollaboration(mode, goal string, runner *subagent.Runner, llm port.ILLMPort) *Collaboration {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeParallel
	}
	specs := ExpandCollaboration(mode, goal)
	now := time.Now()
	cs := &CollaborationState{
		ID:            fmt.Sprintf("collab-%d", now.UnixNano()),
		Mode:          mode,
		Goal:          goal,
		FeedbackCount: 0,
		MaxFeedback:   2,
		Status:        "init",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	phases := make([]*Phase, len(specs))
	for i, s := range specs {
		phases[i] = &Phase{
			ID:    s.ID,
			Name:  s.Role,
			Role:  s.Role,
			State: PhasePending,
			Spec:  s,
		}
	}
	cs.Phases = phases
	return &Collaboration{
		state:   cs,
		runner:  runner,
		llm:     llm,
		specs:   specs,
		results: make(map[string]*subagent.Result),
	}
}

// OnEvent sets the event sink for collaboration progress.
func (c *Collaboration) OnEvent(ch chan<- CollaborationEvent) {
	c.published = ch
}

func (c *Collaboration) emit(ev CollaborationEvent) {
	if c.published != nil {
		select {
		case c.published <- ev:
		default:
		}
	}
}

// State returns a copy of the current collaboration state.
func (c *Collaboration) State() *CollaborationState {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *c.state
	phases := make([]*Phase, len(c.state.Phases))
	for i, p := range c.state.Phases {
		pp := *p
		phases[i] = &pp
	}
	cp.Phases = phases
	return &cp
}

// Run executes the full collaboration workflow with feedback loops.
func (c *Collaboration) Run(ctx context.Context) (*CollaborationState, error) {
	c.mu.Lock()
	if c.state.Status != "init" && c.state.Status != "paused" {
		c.mu.Unlock()
		return nil, fmt.Errorf("collaboration already started or finished: %s", c.state.Status)
	}
	c.state.Status = "running"
	c.state.UpdatedAt = time.Now()
	c.mu.Unlock()

	c.emit(CollaborationEvent{Type: "start", Message: fmt.Sprintf("Starting collaboration mode=%s goal=%s", c.state.Mode, c.state.Goal)})

	switch c.state.Mode {
	case ModeReview:
		return c.runReview(ctx)
	case ModeDebate:
		return c.runDebate(ctx)
	case ModeMerge:
		return c.runMerge(ctx)
	default:
		return c.runParallel(ctx)
	}
}

// runReview implements explore → verify → feedback → fix loop.
func (c *Collaboration) runReview(ctx context.Context) (*CollaborationState, error) {
	phases := c.state.Phases
	if len(phases) < 2 {
		return c.finalize("failed", fmt.Errorf("review mode requires at least 2 phases"))
	}

	explore := phases[0]
	verify := phases[1]

	explore.State = PhaseRunning
	explore.StartedAt = time.Now()
	c.emit(CollaborationEvent{Type: "phase_start", PhaseID: explore.ID, Message: "explore phase started"})

	exploreResult := c.runPhase(ctx, explore)
	if exploreResult != nil && exploreResult.Status == "error" {
		c.mu.Lock()
		c.state.Status = "failed"
		c.mu.Unlock()
		return c.State(), fmt.Errorf("explore phase failed: %s", exploreResult.Output)
	}

	explore.State = PhaseComplete
	explore.CompletedAt = time.Now()

	for feedbackRound := 0; feedbackRound < c.state.MaxFeedback; feedbackRound++ {
		c.mu.Lock()
		c.state.FeedbackCount = feedbackRound + 1
		c.state.UpdatedAt = time.Now()
		c.mu.Unlock()

		verify.State = PhaseFeedback
		verify.StartedAt = time.Now()
		verify.Spec.Prompt = fmt.Sprintf(
			"Review the following explore output for feedback round %d.\nCritique gaps, bugs, and list specific fixes needed.\n\nExplore output:\n%s\n\nGoal: %s",
			feedbackRound+1, exploreResult.Output, c.state.Goal,
		)
		c.emit(CollaborationEvent{
			Type:    "phase_feedback",
			PhaseID: verify.ID,
			Message: fmt.Sprintf("feedback round %d: reviewing explore output", feedbackRound+1),
		})

		verifyResult := c.runPhase(ctx, verify)
		if verifyResult != nil {
			verify.Result = verifyResult
		}

		needsFix := c.needsFix(verifyResult)
		if !needsFix {
			verify.State = PhaseComplete
			verify.CompletedAt = time.Now()
			c.emit(CollaborationEvent{
				Type:    "phase_complete",
				PhaseID: verify.ID,
				Message: fmt.Sprintf("review complete after %d feedback rounds", feedbackRound+1),
			})
			break
		}

		verify.State = PhaseComplete
		verify.CompletedAt = time.Now()
		explore.State = PhaseFeedback
		explore.Feedback = verifyResult.Output
		explore.Spec.Prompt = fmt.Sprintf(
			"Based on the following review feedback, fix the issues.\nMaintain existing functionality while addressing each point.\n\nFeedback:\n%s\n\nOriginal goal: %s",
			verifyResult.Output, c.state.Goal,
		)
		explore.StartedAt = time.Now()
		c.emit(CollaborationEvent{
			Type:    "phase_retry",
			PhaseID: explore.ID,
			Message: fmt.Sprintf("feedback round %d: fixing based on review", feedbackRound+1),
		})

		exploreResult = c.runPhase(ctx, explore)
		explore.State = PhaseComplete
		explore.CompletedAt = time.Now()
		if exploreResult != nil {
			explore.Result = exploreResult
		}
	}

	return c.finalize("completed", nil)
}

// runDebate implements two-role argumentation with LLM-based synthesis.
func (c *Collaboration) runDebate(ctx context.Context) (*CollaborationState, error) {
	phases := c.state.Phases
	if len(phases) < 3 {
		return c.finalize("failed", fmt.Errorf("debate mode requires at least 3 phases"))
	}

	c.mu.Lock()
	c.state.Status = "running"
	c.mu.Unlock()

	for i, p := range phases {
		p.State = PhaseRunning
		p.StartedAt = time.Now()
		c.emit(CollaborationEvent{Type: "phase_start", PhaseID: p.ID, Message: fmt.Sprintf("debate phase %d: %s", i+1, p.Name)})
		result := c.runPhase(ctx, p)
		if result != nil {
			p.Result = result
		}
		p.State = PhaseComplete
		p.CompletedAt = time.Now()
	}

	merge := phases[len(phases)-1]
	merge.State = PhaseRunning
	merge.StartedAt = time.Now()
	merge.Spec.Prompt = c.buildDebateMergePrompt()
	c.emit(CollaborationEvent{Type: "phase_start", PhaseID: merge.ID, Message: "debate merge phase"})
	mergeResult := c.runPhase(ctx, merge)
	if mergeResult != nil {
		merge.Result = mergeResult
	}
	merge.State = PhaseComplete
	merge.CompletedAt = time.Now()

	return c.finalize("completed", nil)
}

// runMerge implements parallel collection followed by a single merge step.
func (c *Collaboration) runMerge(ctx context.Context) (*CollaborationState, error) {
	phases := c.state.Phases
	if len(phases) < 2 {
		return c.finalize("failed", fmt.Errorf("merge mode requires at least 2 phases"))
	}

	workers := phases[:len(phases)-1]
	merger := phases[len(phases)-1]

	c.mu.Lock()
	c.state.Status = "running"
	c.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range workers {
		wg.Add(1)
		go func(phase *Phase) {
			defer wg.Done()
			phase.State = PhaseRunning
			phase.StartedAt = time.Now()
			c.emit(CollaborationEvent{Type: "phase_start", PhaseID: phase.ID, Message: "merge worker started"})
			result := c.runPhase(ctx, phase)
			if result != nil {
				c.mu.Lock()
				phase.Result = result
				c.mu.Unlock()
			}
			phase.State = PhaseComplete
			phase.CompletedAt = time.Now()
		}(p)
	}
	wg.Wait()

	merger.State = PhaseRunning
	merger.StartedAt = time.Now()
	merger.Spec.Prompt = c.buildSynthesisPrompt(workers)
	c.emit(CollaborationEvent{Type: "phase_start", PhaseID: merger.ID, Message: "merge synthesis phase"})
	mergeResult := c.runPhase(ctx, merger)
	if mergeResult != nil {
		merger.Result = mergeResult
	}
	merger.State = PhaseComplete
	merger.CompletedAt = time.Now()

	return c.finalize("completed", nil)
}

// runParallel implements simple parallel execution with optional LLM merge.
func (c *Collaboration) runParallel(ctx context.Context) (*CollaborationState, error) {
	phases := c.state.Phases
	c.mu.Lock()
	c.state.Status = "running"
	c.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range phases {
		wg.Add(1)
		go func(phase *Phase) {
			defer wg.Done()
			phase.State = PhaseRunning
			phase.StartedAt = time.Now()
			c.emit(CollaborationEvent{Type: "phase_start", PhaseID: phase.ID, Message: "parallel agent started"})
			result := c.runPhase(ctx, phase)
			if result != nil {
				c.mu.Lock()
				phase.Result = result
				c.mu.Unlock()
			}
			phase.State = PhaseComplete
			phase.CompletedAt = time.Now()
		}(p)
	}
	wg.Wait()

	allResults := c.collectResults()
	if len(allResults) > 1 && c.llm != nil {
		synthesis := c.synthesizeResults(ctx, allResults)
		if synthesis != "" {
			c.mu.Lock()
			c.state.Goal = synthesis
			c.mu.Unlock()
		}
	}

	return c.finalize("completed", nil)
}

// runPhase executes a single phase via the subagent runner.
func (c *Collaboration) runPhase(ctx context.Context, p *Phase) *subagent.Result {
	if c.runner == nil {
		return &subagent.Result{
			ID: p.Spec.ID, Role: p.Role, Status: "error",
			Output: "no subagent runner available",
		}
	}
	result := c.runner.RunOne(ctx, p.Spec)
	c.mu.Lock()
	c.results[p.Spec.ID] = &result
	c.mu.Unlock()
	return &result
}

// needsFix analyzes verify output to determine if fix is needed.
func (c *Collaboration) needsFix(verifyResult *subagent.Result) bool {
	if verifyResult == nil {
		return false
	}
	output := strings.ToLower(verifyResult.Output)
	if strings.Contains(output, "no issues found") || strings.Contains(output, "looks good") ||
		strings.Contains(output, "all checks passed") || strings.Contains(output, "solid") {
		return false
	}
	negativeSignals := []string{"issues found", "needs fix", "bugs found", "gaps identified", "problems", "errors", "failed"}
	for _, sig := range negativeSignals {
		if strings.Contains(output, sig) {
			return true
		}
	}
	return false
}

// buildDebateMergePrompt creates a prompt for synthesizing debate outputs.
func (c *Collaboration) buildDebateMergePrompt() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Synthesize the following debate into one recommendation.\nGoal: %s\n\n", c.state.Goal))
	for _, p := range c.state.Phases {
		if p.Result != nil {
			b.WriteString(fmt.Sprintf("### %s (role=%s)\n%s\n\n", p.Name, p.Role, p.Result.Output))
		}
	}
	b.WriteString("Provide a clear, actionable final recommendation that incorporates the strongest arguments from each side.")
	return b.String()
}

// buildSynthesisPrompt creates a prompt for merging worker outputs.
func (c *Collaboration) buildSynthesisPrompt(workers []*Phase) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Merge the following worker outputs into a single comprehensive result.\nGoal: %s\n\n", c.state.Goal))
	for _, p := range workers {
		if p.Result != nil {
			b.WriteString(fmt.Sprintf("### %s (role=%s)\n%s\n\n", p.Name, p.Role, p.Result.Output))
		}
	}
	b.WriteString("Combine findings, resolve contradictions, and produce one unified answer.")
	return b.String()
}

// synthesizeResults uses LLM to merge multiple agent outputs.
func (c *Collaboration) synthesizeResults(ctx context.Context, results []*subagent.Result) string {
	if c.llm == nil || len(results) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Synthesize these agent results into a concise final answer.\n\n"))
	for i, r := range results {
		b.WriteString(fmt.Sprintf("Result %d (role=%s):\n%s\n\n", i+1, r.Role, truncateStr(r.Output, 500)))
	}
	b.WriteString("Provide a unified, actionable summary.")

	resp, err := c.llm.Generate(ctx, &port.ChatRequest{
		SystemPrompt: "You are a synthesis engine. Merge multiple agent outputs into one coherent answer.",
		Messages:     []port.ChatMessage{{Role: "user", Content: b.String()}},
		Temperature:  0.3,
		MaxTokens:    500,
	})
	if err != nil || resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Content)
}

// collectResults gathers all phase results.
func (c *Collaboration) collectResults() []*subagent.Result {
	var results []*subagent.Result
	for _, p := range c.state.Phases {
		if p.Result != nil {
			results = append(results, p.Result)
		}
	}
	return results
}

// finalize marks the collaboration as complete.
func (c *Collaboration) finalize(status string, err error) (*CollaborationState, error) {
	c.mu.Lock()
	c.state.Status = status
	c.state.UpdatedAt = time.Now()
	c.mu.Unlock()

	if err != nil {
		c.emit(CollaborationEvent{Type: "error", Message: err.Error()})
	} else {
		c.emit(CollaborationEvent{Type: "complete", Message: fmt.Sprintf("collaboration finished: %s", status)})
	}
	return c.State(), err
}

// FinalOutput assembles the final collaborative result.
func (c *Collaboration) FinalOutput() string {
	state := c.State()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Team Collaboration: %s (mode=%s)\n\n", state.Goal, state.Mode))
	for _, p := range state.Phases {
		if p.Result != nil {
			b.WriteString(fmt.Sprintf("## [%s] %s (role=%s, state=%s)\n%s\n\n", p.ID, p.Name, p.Role, p.State, p.Result.Output))
		}
	}
	b.WriteString(fmt.Sprintf("*Status: %s | Feedback rounds: %d*\n", state.Status, state.FeedbackCount))
	return b.String()
}

// Resume resumes a paused collaboration.
func (c *Collaboration) Resume(ctx context.Context) (*CollaborationState, error) {
	c.mu.Lock()
	if c.state.Status != "paused" {
		c.mu.Unlock()
		return nil, fmt.Errorf("cannot resume: status is %s", c.state.Status)
	}
	c.state.Status = "running"
	c.state.UpdatedAt = time.Now()
	mode := c.state.Mode
	c.mu.Unlock()

	switch mode {
	case ModeReview:
		return c.runReview(ctx)
	case ModeDebate:
		return c.runDebate(ctx)
	case ModeMerge:
		return c.runMerge(ctx)
	default:
		return c.runParallel(ctx)
	}
}

// Cancel cancels a running collaboration.
func (c *Collaboration) Cancel() {
	c.mu.Lock()
	c.state.Status = "cancelled"
	c.state.UpdatedAt = time.Now()
	c.mu.Unlock()
	c.emit(CollaborationEvent{Type: "cancelled", Message: "collaboration cancelled by user"})
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
