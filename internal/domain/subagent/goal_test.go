package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- GoalTracker tests ---

func TestGoalTrackerLifecycle(t *testing.T) {
	gt := NewGoalTracker("g1", "build a feature", 1_000_000)

	if gt.Status() != GoalStatusActive {
		t.Errorf("initial status = %v, want active", gt.Status())
	}
	if gt.Phase() != GoalPhaseIdle {
		t.Errorf("initial phase = %v, want idle", gt.Phase())
	}

	gt.StartPlanning()
	if gt.Phase() != GoalPhasePlanning {
		t.Errorf("phase = %v, want planning", gt.Phase())
	}

	gt.CompletePlanning()
	if gt.Phase() != GoalPhaseExecuting {
		t.Errorf("phase = %v, want executing", gt.Phase())
	}

	gt.StartWorker("implementer")
	if gt.ConsecutiveFailures() != 0 {
		t.Error("failures should be 0")
	}

	gt.FailWorker("error 1")
	if gt.ConsecutiveFailures() != 1 {
		t.Errorf("failures = %d, want 1", gt.ConsecutiveFailures())
	}

	gt.CompleteWorker("done")
	if gt.ConsecutiveFailures() != 0 {
		t.Error("failures should reset after success")
	}

	gt.StartWorker("implementer")
	gt.CompleteWorker("done again")

	gt.Complete()
	if !gt.IsComplete() {
		t.Error("goal should be complete")
	}

	snap := gt.Snapshot()
	if snap.Status != GoalStatusComplete {
		t.Errorf("snap status = %v, want complete", snap.Status)
	}
	if snap.TotalWorkerRounds != 2 {
		t.Errorf("total workers = %d, want 2", snap.TotalWorkerRounds)
	}
	if snap.Objective != "build a feature" {
		t.Errorf("objective = %q", snap.Objective)
	}
}

func TestGoalTrackerPauseReasons(t *testing.T) {
	gt := NewGoalTracker("g2", "test", 100)

	gt.Pause(GoalPauseUser, "user paused")
	if gt.Status() != GoalStatusUserPaused {
		t.Errorf("want user_paused, got %v", gt.Status())
	}
	if !gt.Status().IsPaused() {
		t.Error("should be paused")
	}

	gt.Resume()
	if gt.Status() != GoalStatusActive {
		t.Errorf("want active after resume, got %v", gt.Status())
	}
	if gt.Status().IsPaused() {
		t.Error("should not be paused after resume")
	}

	gt.Pause(GoalPauseBackOff, "budget hit")
	if gt.Status() != GoalStatusBackOffPaused {
		t.Errorf("want back_off_paused")
	}

	gt.Pause(GoalPauseNoProgress, "no progress")
	if gt.Status() != GoalStatusNoProgressPaused {
		t.Errorf("want no_progress_paused")
	}

	gt.Pause(GoalPauseVerification, "verifier blocked")
	if gt.Status() != GoalStatusBlocked {
		t.Errorf("want blocked")
	}

	gt.Pause(GoalPauseInfra, "infra error")
	if gt.Status() != GoalStatusInfraPaused {
		t.Errorf("want infra_paused")
	}
}

func TestGoalTrackerStallDetection(t *testing.T) {
	gt := NewGoalTracker("g3", "stall test", 1000)

	// Different fingerprints → no stall
	if gt.RecordStall("fp-a") {
		t.Error("first unique fingerprint should not trigger stall")
	}
	if gt.RecordStall("fp-b") {
		t.Error("second unique fingerprint should not trigger stall")
	}

	// Same fingerprint twice → stall on second occurrence
	gt.RecordStall("fp-c")
	if gt.RecordStall("fp-c") != true {
		t.Error("second identical fingerprint should trigger stall")
	}
	if gt.Status() != GoalStatusNoProgressPaused {
		t.Errorf("want no_progress_paused, got %v", gt.Status())
	}
}

func TestGapFingerprint(t *testing.T) {
	f1 := GapFingerprint([]string{"missing test", "bad config"})
	f2 := GapFingerprint([]string{"missing test", "bad config"})
	f3 := GapFingerprint([]string{"bad config", "missing test"}) // reordered

	if f1 != f2 {
		t.Error("same gaps should produce same fingerprint")
	}
	if f1 == f3 {
		t.Error("reordered gaps should produce different fingerprint")
	}
	if GapFingerprint(nil) != "" {
		t.Error("empty gaps should return empty fingerprint")
	}
}

func TestGoalTrackerTokenBudget(t *testing.T) {
	gt := NewGoalTracker("g4", "budget test", 100)

	exceeded := gt.RecordTokens(50)
	if exceeded {
		t.Error("50 < 100 should not exceed")
	}
	if gt.TokensUsed() != 50 {
		t.Errorf("tokens = %d, want 50", gt.TokensUsed())
	}

	exceeded = gt.RecordTokens(60)
	if !exceeded {
		t.Error("50+60 > 100 should exceed")
	}
	if gt.Status() != GoalStatusBudgetLimited {
		t.Errorf("want budget_limited, got %v", gt.Status())
	}
}

func TestGoalTrackerEventHistory(t *testing.T) {
	gt := NewGoalTracker("g5", "history test", 100)
	gt.StartPlanning()
	gt.CompletePlanning()
	gt.StartWorker("explore")
	gt.CompleteWorker("done")
	gt.Complete()

	h := gt.History()
	if len(h) < 5 {
		t.Errorf("history len = %d, want >=5", len(h))
	}

	eventSet := map[GoalEvent]bool{}
	for _, e := range h {
		eventSet[e.Event] = true
	}
	for _, ev := range []GoalEvent{
		GoalEventPlanningStarted, GoalEventPlanningCompleted,
		GoalEventWorkerStarted, GoalEventWorkerCompleted, GoalEventGoalCompleted,
	} {
		if !eventSet[ev] {
			t.Errorf("missing event: %v", ev)
		}
	}
}

// --- PrematureStopDetector tests ---

func TestPrematureStopDetector(t *testing.T) {
	d := NewPrematureStopDetector()

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "giving up",
			text: "Giving up, the task is too complex.",
			want: "giving_up",
		},
		{
			name: "can't proceed",
			text: "I can't proceed without user input.\n\nOther paragraph.",
			want: "unable_to_proceed",
		},
		{
			name: "stopping here",
			text: "Stopping here, will resume tomorrow.",
			want: "stopping_here",
		},
		{
			name: "verdict pass",
			text: "VERDICT: PASS\nAll criteria met.",
			want: "verdict_line",
		},
		{
			name: "ready for review",
			text: "Ready for review.\n\nThe code is complete.",
			want: "ready_for_review",
		},
		{
			name: "normal work",
			text: "I've completed the implementation.\n\nThe tests pass and the build is green.",
			want: "",
		},
		{
			name: "in-prose mention ignored",
			text: "Once the test settles I'll iterate.\n\nGood progress so far.",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.Detect(tt.text)
			if got != tt.want {
				t.Errorf("Detect(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestPrematureStopDetectorAllPatterns(t *testing.T) {
	d := NewPrematureStopDetector()
	patterns := d.AllPatterns()
	if len(patterns) == 0 {
		t.Error("should have patterns")
	}
}

// --- RoleToolNames tests ---

func TestRoleToolNamesApply(t *testing.T) {
	rtn := NewRoleToolNames("my_read", "", "", "", "", "", "", "")
	rtn.SetToolset("- tool1\n- tool2")

	tmpl := "Read with {READ_TOOL}. Search via {SEARCH_TOOL}. Tools: {TOOLSET_TOOLS}"
	out := rtn.Apply(tmpl)

	if out == tmpl {
		t.Error("placeholders should be substituted")
	}
	if !contains(out, "my_read") {
		t.Error("should have substituted read")
	}
	if !contains(out, "grep") {
		t.Error("search should fall back to grep")
	}
	if !contains(out, "tool1") {
		t.Error("toolset should be included")
	}
}

func TestRoleToolNamesUnsafeRejected(t *testing.T) {
	rtn := NewRoleToolNames("bad name", "ok_list", "", "", "", "", "", "")
	if rtn.Read != defaultRead {
		t.Errorf("unsafe read name %q should fall back to %q", rtn.Read, defaultRead)
	}
	if rtn.List != "ok_list" {
		t.Errorf("safe list name should be kept")
	}
}

// --- SpawnWithFailOpenRetry tests ---

func TestSpawnWithFailOpenRetryInherit(t *testing.T) {
	called := 0
	spawn := func(ctx context.Context, modelID, harnessID, prompt string) (string, *SpawnError) {
		called++
		if modelID != "" || harnessID != "" {
			return "", &SpawnError{Message: "should not be called with override"}
		}
		return "result", nil
	}

	out, err := SpawnWithFailOpenRetry(context.Background(), "test",
		RoleSpawnOverride{},
		RoleRenderedPrompt{Primary: "prompt"},
		spawn)

	if out != "result" || err != nil {
		t.Errorf("unexpected: out=%q err=%v", out, err)
	}
	if called != 1 {
		t.Errorf("called %d times, want 1", called)
	}
}

func TestSpawnWithFailOpenRetryFailOpen(t *testing.T) {
	called := 0
	spawn := func(ctx context.Context, modelID, harnessID, prompt string) (string, *SpawnError) {
		called++
		if called == 1 {
			return "", &SpawnError{Message: "spawn failed"}
		}
		if modelID != "" {
			return "", &SpawnError{Message: "retry should be inherit"}
		}
		return "recovered", nil
	}

	out, err := SpawnWithFailOpenRetry(context.Background(), "test",
		RoleSpawnOverride{ModelID: "grok-3"},
		RoleRenderedPrompt{Primary: "primary", Fallback: "fallback"},
		spawn)

	if out != "recovered" || err != nil {
		t.Errorf("unexpected: out=%q err=%v", out, err)
	}
	if called != 2 {
		t.Errorf("called %d times, want 2", called)
	}
}

func TestSpawnWithFailOpenRetryCancelled(t *testing.T) {
	called := 0
	spawn := func(ctx context.Context, modelID, harnessID, prompt string) (string, *SpawnError) {
		called++
		return "", &SpawnError{Message: "cancelled", Cancelled: true}
	}

	_, err := SpawnWithFailOpenRetry(context.Background(), "test",
		RoleSpawnOverride{ModelID: "grok-3"},
		RoleRenderedPrompt{Primary: "primary", Fallback: "fallback"},
		spawn)

	if err == nil || !err.IsCancelled() {
		t.Error("cancellation should propagate without retry")
	}
	if called != 1 {
		t.Errorf("called %d times, want 1 (no retry on cancel)", called)
	}
}

// --- GoalOrchestrator tests ---

func TestGoalOrchestratorCompleteFlow(t *testing.T) {
	cfg := DefaultOrchestratorConfig()
	cfg.Planner = func(ctx context.Context, objective, context string) (*GoalPlan, error) {
		return &GoalPlan{
			ID: "plan-1", Objective: objective, Kind: GoalKindCodeChange,
			AcceptanceCriteria: []string{"criteria met"},
			VerificationPlan:   []string{"verify"},
			CreatedAt:          time.Now(), LastUpdatedAt: time.Now(),
		}, nil
	}
	cfg.Implementer = func(ctx context.Context, plan *GoalPlan) (string, error) {
		return "work done", nil
	}
	cfg.Verifier = func(ctx context.Context, plan *GoalPlan, workOutput string) (VerifierResult, error) {
		return VerifierResult{Passed: true, Reason: "criteria met", Evidence: "all good"}, nil
	}
	cfg.SkepticCount = 2

	orch := NewGoalOrchestrator(cfg)
	result, err := orch.Execute(context.Background(), "build a feature", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Tracker.IsComplete() {
		t.Errorf("tracker should be complete, status=%v", result.Tracker.Status())
	}
	if len(result.PanelResults) != 1 {
		t.Errorf("want 1 panel result, got %d", len(result.PanelResults))
	}
	if !result.PanelResults[0].Passed {
		t.Error("panel should pass")
	}
	if len(result.PanelResults[0].Skeptics) != 2 {
		t.Errorf("want 2 skeptics, got %d", len(result.PanelResults[0].Skeptics))
	}
}

func TestGoalOrchestratorFailLoopAndComplete(t *testing.T) {
	cfg := DefaultOrchestratorConfig()
	cfg.SkepticCount = 2
	cfg.MaxVerifyRuns = 3
	cfg.StrategistAfterFailures = 2

	var mu sync.Mutex
	callCount := 0
	cfg.Planner = func(ctx context.Context, objective, context string) (*GoalPlan, error) {
		return &GoalPlan{
			ID: "plan-2", Objective: objective, Kind: GoalKindCodeChange,
			AcceptanceCriteria: []string{"criteria met"},
			VerificationPlan:   []string{"verify"},
			CreatedAt:          time.Now(), LastUpdatedAt: time.Now(),
		}, nil
	}
	cfg.Implementer = func(ctx context.Context, plan *GoalPlan) (string, error) {
		return "work", nil
	}
	cfg.Verifier = func(ctx context.Context, plan *GoalPlan, workOutput string) (VerifierResult, error) {
		mu.Lock()
		callCount++
		current := callCount
		mu.Unlock()
		if current < 5 {
			return VerifierResult{
				Passed: false, Reason: "not yet", Evidence: "fail",
				Gaps: []string{fmt.Sprintf("gap-%d", current)},
			}, nil
		}
		return VerifierResult{Passed: true, Reason: "done", Evidence: "success"}, nil
	}

	orch := NewGoalOrchestrator(cfg)
	result, err := orch.Execute(context.Background(), "build feature", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Tracker.IsComplete() {
		t.Errorf("tracker should be complete after retry, status=%v", result.Tracker.Status())
	}
	// Should have at least 3 panel results (fail, fail, pass)
	if len(result.PanelResults) < 3 {
		t.Errorf("want >=3 panel results, got %d", len(result.PanelResults))
	}
}

func TestGoalOrchestratorStallDetection(t *testing.T) {
	cfg := DefaultOrchestratorConfig()
	cfg.SkepticCount = 1
	cfg.MaxVerifyRuns = 10

	// Use a mutex to ensure deterministic verifier behavior
	var mu sync.Mutex
	callCount := 0
	cfg.Planner = func(ctx context.Context, objective, context string) (*GoalPlan, error) {
		return &GoalPlan{
			ID: "plan-3", Objective: objective, Kind: GoalKindCodeChange,
			AcceptanceCriteria: []string{"criteria"},
			VerificationPlan:   []string{"verify"},
			CreatedAt:          time.Now(), LastUpdatedAt: time.Now(),
		}, nil
	}
	cfg.Implementer = func(ctx context.Context, plan *GoalPlan) (string, error) {
		return "same output", nil
	}
	// Always fail with same gaps (synchronized to avoid race)
	cfg.Verifier = func(ctx context.Context, plan *GoalPlan, workOutput string) (VerifierResult, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return VerifierResult{
			Passed: false, Reason: "gap", Evidence: "fail",
			Gaps: []string{"always same gap"},
		}, nil
	}

	orch := NewGoalOrchestrator(cfg)
	result, err := orch.Execute(context.Background(), "build", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Stall should be detected within 3 iterations (first gap sets fp, second same fp triggers stall)
	// But stall threshold is 2 so we need at least 3 iterations before auto-pause
	if result.Tracker.Status() != GoalStatusNoProgressPaused {
		t.Errorf("want no_progress_paused, got %v (callCount=%d)", result.Tracker.Status(), callCount)
	}
}

func TestGoalOrchestratorCancellation(t *testing.T) {
	cfg := DefaultOrchestratorConfig()
	cfg.SkepticCount = 1
	cfg.Planner = func(ctx context.Context, objective, context string) (*GoalPlan, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		time.Sleep(100 * time.Millisecond)
		return &GoalPlan{
			ID: "plan-4", Objective: objective, Kind: GoalKindCodeChange,
			AcceptanceCriteria: []string{"criteria"},
			VerificationPlan:   []string{"verify"},
			CreatedAt:          time.Now(), LastUpdatedAt: time.Now(),
		}, nil
	}
	cfg.Implementer = func(ctx context.Context, plan *GoalPlan) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		return "work", nil
	}
	cfg.Verifier = func(ctx context.Context, plan *GoalPlan, workOutput string) (VerifierResult, error) {
		return VerifierResult{Passed: true, Reason: "done", Evidence: "ok"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	orch := NewGoalOrchestrator(cfg)

	var wg sync.WaitGroup
	var result *GoalExecutionResult
	var runErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, runErr = orch.Execute(ctx, "build", "")
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	if result != nil && result.Tracker.Status() != GoalStatusUserPaused {
		t.Errorf("want user_paused or err, got status=%v err=%v", result.Tracker.Status(), runErr)
	}
}

func TestGoalOrchestratorDefaultNoFunctions(t *testing.T) {
	cfg := DefaultOrchestratorConfig()
	cfg.Planner = nil
	cfg.Implementer = nil
	cfg.Verifier = nil

	orch := NewGoalOrchestrator(cfg)
	result, err := orch.Execute(context.Background(), "build simple", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Tracker.IsComplete() {
		t.Errorf("auto-default should complete, got %v", result.Tracker.Status())
	}
	if len(result.PanelResults) != 1 {
		t.Errorf("want 1 default panel, got %d", len(result.PanelResults))
	}
	if !result.PanelResults[0].Passed {
		t.Error("default panel should pass")
	}
}

// --- BuildPlannerPrompt / BuildVerifierPrompt tests ---

func TestBuildPlannerPrompt(t *testing.T) {
	tools := DefaultToolNames()
	p := BuildPlannerPrompt("build a parser", "context here", tools)
	if p == "" {
		t.Error("prompt should not be empty")
	}
	if !contains(p, "build a parser") {
		t.Error("should contain objective")
	}
	if !contains(p, "read_file") {
		t.Error("should contain default tool name")
	}
	if !contains(p, "context here") {
		t.Error("should contain context")
	}
}

func TestBuildVerifierPrompt(t *testing.T) {
	plan := &GoalPlan{
		ID: "p1", Objective: "build API", Kind: GoalKindCodeChange,
		AcceptanceCriteria: []string{"endpoint works", "tests pass"},
		VerificationPlan:   []string{"curl endpoint", "run tests"},
	}
	tools := DefaultToolNames()
	p := BuildVerifierPrompt(plan, "work output", tools)
	if !contains(p, "endpoint works") {
		t.Error("should contain criteria")
	}
	if !contains(p, "work output") {
		t.Error("should contain work output")
	}
	if !contains(p, "code-change") {
		t.Error("should contain kind")
	}
}

// --- parsePlanResponse tests ---

func TestParsePlanResponse(t *testing.T) {
	content := `## Goal kind
code-change

## Acceptance criteria
1. Feature X works
2. Tests pass

## Verification plan
1. Run tests
2. Manual verify

## Non-goals
- Polish items
`
	plan, err := parsePlanResponse(content, "build feature")
	if err != nil {
		t.Fatalf("parsePlanResponse: %v", err)
	}
	if plan.Objective != "build feature" {
		t.Errorf("objective = %q", plan.Objective)
	}
	if plan.Kind != GoalKindCodeChange {
		t.Errorf("kind = %v", plan.Kind)
	}
	if len(plan.AcceptanceCriteria) < 2 {
		t.Errorf("criteria = %v, want >=2", plan.AcceptanceCriteria)
	}
	if len(plan.VerificationPlan) < 2 {
		t.Errorf("verification = %v, want >=2", plan.VerificationPlan)
	}
	if len(plan.NonGoals) == 0 {
		t.Error("non-goals should be parsed")
	}
}

// --- parseVerifierResponse tests ---

func TestParseVerifierResponse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"explicit pass", `{"passed": true, "reason": "criteria met", "evidence": "tests pass"}`, true},
		{"explicit fail", `{"passed": false, "reason": "missing", "evidence": "nope"}`, false},
		{"keyword pass", "All criteria met. Success.", true},
		{"keyword fail", "Some tests fail.", false},
		{"verdict pass", "VERDICT: PASS", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := parseVerifierResponse(tt.content)
			if r.Passed != tt.want {
				t.Errorf("Passed = %v, want %v", r.Passed, tt.want)
			}
		})
	}
}

// Helpers --------------------------------------------------------------

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure error types satisfy error interface
var _ error = (*SpawnError)(nil)

func TestSpawnError_Error(t *testing.T) {
	e := &SpawnError{Message: "test", Cancelled: true}
	if !contains(e.Error(), "test") {
		t.Error("Error() should contain message")
	}
	if !contains(e.Error(), "cancelled=true") {
		t.Error("Error() should note cancellation")
	}

	e2 := &SpawnError{Message: "oops"}
	if e2.IsCancelled() {
		t.Error("non-cancelled error should not be cancelled")
	}
	if !errors.Is(nil, nil) {
		// just ensure errors package is imported
	}
}
