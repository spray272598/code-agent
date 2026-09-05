// Experimental: part of the GoalOrchestrator subsystem (plan→execute→verify).
// Not wired into the default agent runtime yet; treat as a spike, API may churn.
package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GoalKind identifies the nature of the goal.
type GoalKind int

const (
	GoalKindCodeChange GoalKind = iota
	GoalKindAnalysis
	GoalKindResearch
)

// String returns a human label for GoalKind.
func (k GoalKind) String() string {
	switch k {
	case GoalKindAnalysis:
		return "analysis"
	case GoalKindResearch:
		return "research"
	default:
		return "code-change"
	}
}

// GoalPlan is the structured plan written by the planner subagent.
type GoalPlan struct {
	ID                 string    `json:"id"`
	Objective          string    `json:"objective"`
	Kind               GoalKind  `json:"kind"`
	AcceptanceCriteria []string  `json:"acceptanceCriteria"`
	VerificationPlan   []string  `json:"verificationPlan"`
	NonGoals           []string  `json:"nonGoals,omitempty"`
	AssumedScope       string    `json:"assumedScope,omitempty"`
	ImplementationNote string    `json:"implementationNote,omitempty"`
	TaskChecklist      []string  `json:"taskChecklist,omitempty"`
	Risks              []string  `json:"risks,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	LastUpdatedAt      time.Time `json:"lastUpdatedAt"`
	CurrentStep        int       `json:"currentStep"`
}

// VerifierResult is a single skeptic's verdict.
type VerifierResult struct {
	SkepticIndex int      `json:"skepticIndex"`
	Role         string   `json:"role"`
	Passed       bool     `json:"passed"`
	Reason       string   `json:"reason"`
	Evidence     string   `json:"evidence"`
	Gaps         []string `json:"gaps,omitempty"`
}

// VerificationPanelResult is the aggregated verdict of N skeptics.
type VerificationPanelResult struct {
	Attempt   int              `json:"attempt"`
	Skeptics  []VerifierResult `json:"skeptics"`
	Passed    bool             `json:"passed"`
	Reason    string           `json:"reason"`
	Majority  int              `json:"majority"`
	Failed    int              `json:"failed"`
	Gaps      []string         `json:"gaps,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
}

// GoalExecutable is the interface the orchestrator calls to run subagents.
// Separated from Runner so the orchestrator can be unit-tested without LLM.
type GoalExecutable interface {
	RunSpec(ctx context.Context, spec Spec) Result
	RunSpecs(ctx context.Context, specs []Spec) []Result
}

// PlannerFunc generates a plan from an objective and context.
type PlannerFunc func(ctx context.Context, objective, context string) (*GoalPlan, error)

// VerifierFunc evaluates whether the deliverable meets acceptance criteria.
type VerifierFunc func(ctx context.Context, plan *GoalPlan, workOutput string) (VerifierResult, error)

// ImplementerFunc executes a plan step, returning the output.
type ImplementerFunc func(ctx context.Context, plan *GoalPlan) (string, error)

// OrchestratorConfig configures the GoalOrchestrator.
type OrchestratorConfig struct {
	// Planner generates the plan on goal create.
	Planner PlannerFunc
	// Implementer executes the plan and returns the work transcript.
	Implementer ImplementerFunc
	// Verifier evaluates deliverable against acceptance criteria.
	Verifier VerifierFunc
	// SkepticCount is the number of parallel skeptics (default 3).
	SkepticCount int
	// MaxVerifyRuns is the cap on verification attempts (default 10).
	MaxVerifyRuns int
	// StallThreshold (default 2) consecutive identical gap fingerprints.
	StallThreshold int
	// Strategist fires after N consecutive failures (default 3).
	StrategistAfterFailures int
	// Token budget for the goal.
	TokenBudget int64
	// OnProgress receives orchestration progress events.
	OnProgress func(Progress)
	// SnapshotManager manages plan snapshots for safety verification.
	SnapshotManager *StrategistSnapshotManager
	// PlanDir is the directory for plan files (used for snapshotting).
	PlanDir string
}

// DefaultOrchestratorConfig returns a config with sensible defaults.
func DefaultOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		SkepticCount:            3,
		MaxVerifyRuns:           10,
		StallThreshold:          2,
		StrategistAfterFailures: 3,
		TokenBudget:             2_000_000,
		SnapshotManager:         NewStrategistSnapshotManager(10),
	}
}

// GoalOrchestrator drives the plan→execute→verify loop.
//
// Lifecycle:
//  1. Create a GoalTracker with the user's objective.
//  2. Planner subagent writes a structured plan.
//  3. Implementer subagent executes the plan.
//  4. N parallel Verifier (skeptic) subagents evaluate deliverable.
//  5. Aggregate verdicts; if majority pass → Complete; else record gaps.
//  6. Detect stall (repeated gap fingerprint) or premature stop.
//  7. If strategist threshold hit → fire Strategist subagent for restructure advice.
//  8. Loop until complete, paused, or budget exhausted.
type GoalOrchestrator struct {
	cfg *OrchestratorConfig
}

// NewGoalOrchestrator creates a new orchestrator.
func NewGoalOrchestrator(cfg *OrchestratorConfig) *GoalOrchestrator {
	if cfg == nil {
		cfg = DefaultOrchestratorConfig()
	}
	if cfg.SkepticCount <= 0 {
		cfg.SkepticCount = 3
	}
	if cfg.MaxVerifyRuns <= 0 {
		cfg.MaxVerifyRuns = 10
	}
	if cfg.StallThreshold <= 0 {
		cfg.StallThreshold = 2
	}
	if cfg.StrategistAfterFailures <= 0 {
		cfg.StrategistAfterFailures = 3
	}
	return &GoalOrchestrator{cfg: cfg}
}

// GoalExecutionResult is the final outcome of the orchestrator.
type GoalExecutionResult struct {
	Tracker         *GoalTracker
	Plan            *GoalPlan
	FinalOutput     string
	PanelResults    []VerificationPanelResult
	StrategistNotes []string
	Errors          []string
	DurationMs      int64
}

// Execute runs the full goal orchestration lifecycle.
func (o *GoalOrchestrator) Execute(ctx context.Context, objective, context string) (*GoalExecutionResult, error) {
	start := time.Now()
	tracker := NewGoalTracker("goal-"+fmt.Sprintf("%d", time.Now().UnixNano()%1e9), objective, o.cfg.TokenBudget)
	result := &GoalExecutionResult{Tracker: tracker}

	// Phase 1: Planning
	tracker.StartPlanning()
	o.emit(Progress{ID: tracker.ID(), Role: "planner", Status: "start", Message: "generating plan..."})

	if o.cfg.Planner == nil {
		tracker.CompletePlanning()
		result.Plan = &GoalPlan{
			ID: "plan-" + tracker.ID(), Objective: objective, Kind: GoalKindCodeChange,
			AcceptanceCriteria: []string{"deliverable matches objective"},
			VerificationPlan:   []string{"verify against criteria"},
			CreatedAt:          time.Now(), LastUpdatedAt: time.Now(),
		}
	} else {
		plan, err := o.cfg.Planner(ctx, objective, context)
		if err != nil {
			tracker.FailPlanning(err.Error())
			result.Errors = append(result.Errors, "planner failed: "+err.Error())
			return result, err
		}
		if plan == nil {
			plan = &GoalPlan{
				ID: "plan-" + tracker.ID(), Objective: objective, Kind: GoalKindCodeChange,
				AcceptanceCriteria: []string{"deliverable matches objective"},
				VerificationPlan:   []string{"verify against criteria"},
				CreatedAt:          time.Now(), LastUpdatedAt: time.Now(),
			}
		}
		plan.ID = "plan-" + tracker.ID()
		plan.LastUpdatedAt = time.Now()
		result.Plan = plan
		tracker.CompletePlanning()
	}

	// Phase 1.5: Create initial plan snapshot for safety
	planPath := o.writePlanFile(tracker.ID(), result.Plan)
	if planPath != "" && o.cfg.SnapshotManager != nil {
		if _, err := o.cfg.SnapshotManager.CreateSnapshot(planPath, "initial plan after planning"); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("snapshot creation failed: %v", err))
		}
	}

	// Phase 2: Execute + Verify loop
	detector := NewPrematureStopDetector()
	stallFingerprint := ""

	for verifyAttempt := 1; verifyAttempt <= o.cfg.MaxVerifyRuns; verifyAttempt++ {
		if ctx.Err() != nil {
			tracker.Pause(GoalPauseUser, ctx.Err().Error())
			result.Errors = append(result.Errors, ctx.Err().Error())
			break
		}
		if tracker.TokensUsed() > o.cfg.TokenBudget {
			tracker.Pause(GoalPauseBackOff, "token budget exceeded")
			result.Errors = append(result.Errors, "token budget exceeded")
			break
		}

		// Execute
		tracker.StartWorker("implementer")
		o.emit(Progress{
			ID: tracker.ID(), Role: "implementer", Status: "start",
			Message: fmt.Sprintf("execution attempt %d", verifyAttempt),
		})

		var output string
		if o.cfg.Implementer == nil {
			output = "done"
		} else {
			out, err := o.cfg.Implementer(ctx, result.Plan)
			if err != nil {
				tracker.FailWorker(err.Error())
				result.Errors = append(result.Errors, err.Error())
				continue
			}
			output = out
		}

		// Detect premature stop
		if pattern := detector.Detect(output); pattern != "" {
			tracker.PrematureStop(pattern)
			o.emit(Progress{
				ID: tracker.ID(), Role: "implementer", Status: "warning",
				Message: fmt.Sprintf("premature-stop pattern: %s", pattern),
			})
		}

		tracker.CompleteWorker("work completed")

		// Verify
		panel := o.runVerificationPanel(ctx, tracker, result.Plan, output, verifyAttempt)
		result.PanelResults = append(result.PanelResults, panel)

		if panel.Passed {
			o.emit(Progress{
				ID: tracker.ID(), Role: "verifier", Status: "done",
				Message: fmt.Sprintf("verification passed on attempt %d", verifyAttempt),
			})
			tracker.Complete()
			break
		}

		// Stall detection
		if len(panel.Gaps) > 0 {
			fp := GapFingerprint(panel.Gaps)
			if fp != "" && fp == stallFingerprint {
				tracker.RecordStall(fp)
				if tracker.Status() == GoalStatusNoProgressPaused {
					result.Errors = append(result.Errors, "stall detected: repeated gaps")
					break
				}
			} else {
				stallFingerprint = fp
				tracker.RecordStall(fp)
			}
		}

		// Strategist trigger with safety snapshot
		if tracker.ConsecutiveFailures() >= o.cfg.StrategistAfterFailures {
			if tracker.StrategistFired(3) {
				// Create safety snapshot before strategist modifies the plan
				if planPath != "" && o.cfg.SnapshotManager != nil {
					if _, err := o.cfg.SnapshotManager.CreateSnapshot(planPath, fmt.Sprintf("pre-strategist snapshot attempt %d", verifyAttempt)); err != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("pre-strategist snapshot failed: %v", err))
					}
				}

				o.emit(Progress{
					ID: tracker.ID(), Role: "strategist", Status: "start",
					Message: "strategist fired for stall recovery",
				})
				note := o.runStrategist(ctx, result.Plan, output, panel)
				if note != "" {
					result.StrategistNotes = append(result.StrategistNotes, note)
				}

				// Verify plan integrity after strategist intervention
				if planPath != "" && o.cfg.SnapshotManager != nil {
					matched, err := o.cfg.SnapshotManager.VerifyIntegrity(planPath)
					if err != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("post-strategist integrity check failed: %v", err))
					}
					if matched == nil {
						// Plan was modified outside snapshot tracking - create new snapshot
						if _, err := o.cfg.SnapshotManager.CreateSnapshot(planPath, fmt.Sprintf("post-strategist snapshot attempt %d", verifyAttempt)); err != nil {
							result.Errors = append(result.Errors, fmt.Sprintf("post-strategist snapshot failed: %v", err))
						}
					}
				}
			}
		}
	}

	if !tracker.IsComplete() && !tracker.Status().IsPaused() {
		tracker.Pause(GoalPauseBackOff, "max verification rounds reached")
	}

	result.FinalOutput = ""
	if len(result.PanelResults) > 0 {
		last := result.PanelResults[len(result.PanelResults)-1]
		for _, s := range last.Skeptics {
			result.FinalOutput += fmt.Sprintf("[%s] pass=%v %s: %s\n",
				s.Role, s.Passed, s.Reason, s.Evidence)
		}
	}
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// writePlanFile writes the plan to a file for snapshot tracking.
// Returns the file path or empty string if PlanDir is not configured.
func (o *GoalOrchestrator) writePlanFile(trackerID string, plan *GoalPlan) string {
	if o.cfg.PlanDir == "" || plan == nil {
		return ""
	}

	planPath := filepath.Join(o.cfg.PlanDir, trackerID+"_plan.md")
	content := fmt.Sprintf("# %s\n\n## Objective\n%s\n\n## Tasks\n", plan.ID, plan.ID)
	for i, task := range plan.TaskChecklist {
		content += fmt.Sprintf("%d. %s\n", i+1, task)
	}
	if len(plan.TaskChecklist) == 0 {
		content += "- [ ] implement core requirements\n"
	}

	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		return ""
	}
	return planPath
}

// runVerificationPanel spawns N parallel skeptics and aggregates verdicts.
func (o *GoalOrchestrator) runVerificationPanel(ctx context.Context, tracker *GoalTracker,
	plan *GoalPlan, workOutput string, attempt int,
) VerificationPanelResult {
	n := o.cfg.SkepticCount
	skeptics := make([]VerifierResult, n)
	ch := make(chan VerifierResult, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			if o.cfg.Verifier == nil {
				ch <- VerifierResult{
					SkepticIndex: idx, Role: "skeptic", Passed: true,
					Reason: "default verifier pass", Evidence: "no verifier configured",
				}
				return
			}
			res, err := o.cfg.Verifier(ctx, plan, workOutput)
			if err != nil {
				ch <- VerifierResult{
					SkepticIndex: idx, Role: "skeptic", Passed: false,
					Reason: "verifier error", Evidence: err.Error(),
				}
				return
			}
			res.SkepticIndex = idx
			res.Role = fmt.Sprintf("skeptic-%d", idx)
			ch <- res
		}(i)
	}

	for i := 0; i < n; i++ {
		select {
		case r := <-ch:
			skeptics[r.SkepticIndex] = r
		case <-ctx.Done():
			skeptics[i] = VerifierResult{
				SkepticIndex: i, Role: "skeptic", Passed: false,
				Reason: "context cancelled", Evidence: ctx.Err().Error(),
			}
		}
	}

	passed, failed := 0, 0
	var allGaps []string
	for _, s := range skeptics {
		if s.Passed {
			passed++
		} else {
			failed++
			allGaps = append(allGaps, s.Gaps...)
		}
	}
	majority := (n / 2) + 1
	panel := VerificationPanelResult{
		Attempt:   attempt,
		Skeptics:  skeptics,
		Passed:    passed >= majority,
		Reason:    fmt.Sprintf("%d/%d skeptics passed (majority=%d)", passed, n, majority),
		Majority:  majority,
		Failed:    failed,
		Gaps:      allGaps,
		Timestamp: time.Now(),
	}
	tracker.RecordVerify()
	return panel
}

// runStrategist fires the strategist to get structural restructure advice.
func (o *GoalOrchestrator) runStrategist(_ context.Context, plan *GoalPlan, workOutput string,
	panel VerificationPanelResult,
) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Strategist Analysis (attempt %d)\n", panel.Attempt))
	b.WriteString(fmt.Sprintf("**Gaps:** %v\n\n", panel.Gaps))
	b.WriteString(fmt.Sprintf("**Consecutive failures:** goal restructured\n"))
	_ = plan
	_ = workOutput
	return b.String()
}

func (o *GoalOrchestrator) emit(p Progress) {
	if o.cfg != nil && o.cfg.OnProgress != nil {
		o.cfg.OnProgress(p)
	}
}
