package team

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

func newTestRunner() *subagent.Runner {
	reg := tool.NewRegistry()
	reg.Register(&testTool{})
	return subagent.NewRunner(nil, reg, "/tmp")
}

type testTool struct{}

func (t *testTool) Name() string               { return "test_tool" }
func (t *testTool) Description() string        { return "test tool" }
func (t *testTool) InputSchema() map[string]any { return map[string]any{} }
func (t *testTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	return tool.Result{Text: "test output"}, nil
}

type mockLLM struct {
	resp *port.ChatResponse
	err  error
}

func (m *mockLLM) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	return m.resp, m.err
}

func (m *mockLLM) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(delta port.StreamDelta)) (*port.ChatResponse, error) {
	return m.resp, m.err
}

func TestNewCollaboration(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "test goal", runner, nil)
	if c == nil {
		t.Fatal("expected collaboration, got nil")
	}
	state := c.State()
	if state.Mode != ModeParallel {
		t.Errorf("expected mode=%s, got %s", ModeParallel, state.Mode)
	}
	if state.Goal != "test goal" {
		t.Errorf("expected goal='test goal', got '%s'", state.Goal)
	}
	if len(state.Phases) == 0 {
		t.Error("expected phases to be populated")
	}
	if state.Status != "init" {
		t.Errorf("expected status=init, got %s", state.Status)
	}
}

func TestCollaborationStateTransitions(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeReview, "review goal", runner, nil)
	state := c.State()

	for _, p := range state.Phases {
		if p.State != PhasePending {
			t.Errorf("expected initial phase state=pending, got %s", p.State)
		}
	}

	c.mu.Lock()
	stateCopy := c.state
	stateCopy.Phases[0].State = PhaseRunning
	c.mu.Unlock()

	state = c.State()
	if state.Phases[0].State != PhaseRunning {
		t.Errorf("expected phase state=running, got %s", state.Phases[0].State)
	}
}

func TestCollaborationFinalOutput(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "output test", runner, nil)

	c.mu.Lock()
	c.state.Phases[0].Result = &subagent.Result{
		ID: "test-1", Role: "explore", Status: "ok",
		Output: "explore found something", Steps: 3,
	}
	c.state.Phases[1].Result = &subagent.Result{
		ID: "test-2", Role: "verify", Status: "ok",
		Output: "verify confirmed", Steps: 2,
	}
	c.mu.Unlock()

	output := c.FinalOutput()
	if output == "" {
		t.Error("expected non-empty final output")
	}
	if !containsStr(output, "explore found something") {
		t.Error("output missing explore result")
	}
	if !containsStr(output, "verify confirmed") {
		t.Error("output missing verify result")
	}
}

func TestNeedsFixDetection(t *testing.T) {
	c := &Collaboration{}
	tests := []struct {
		output   string
		needsFix bool
	}{
		{"No issues found, looks good", false},
		{"Issues found in the implementation", true},
		{"The code needs fix before merging", true},
		{"Bugs found: null pointer dereference", true},
		{"Gaps identified in error handling", true},
		{"Problems with the test coverage", true},
		{"All checks passed", false},
		{"Implementation looks solid", false},
	}

	for _, tt := range tests {
		result := &subagent.Result{Output: tt.output}
		got := c.needsFix(result)
		if got != tt.needsFix {
			t.Errorf("needsFix(%q) = %v, want %v", tt.output, got, tt.needsFix)
		}
	}
}

func TestCancelCollaboration(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "cancel test", runner, nil)
	c.Cancel()
	state := c.State()
	if state.Status != "cancelled" {
		t.Errorf("expected status=cancelled, got %s", state.Status)
	}
}

func TestSynthesizeResults(t *testing.T) {
	mockLLM := &mockLLM{
		resp: &port.ChatResponse{
			Content:     "synthesized answer",
			TotalTokens: common.EstimateTokens("synthesized answer"),
		},
	}
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "synth test", runner, mockLLM)
	results := []*subagent.Result{
		{Output: "result A"},
		{Output: "result B"},
	}
	synth := c.synthesizeResults(context.Background(), results)
	if synth == "" {
		t.Error("expected non-empty synthesis")
	}
}

func TestExpandCollaborationModes(t *testing.T) {
	tests := []struct {
		mode     string
		goal     string
		wantSpec int
	}{
		{ModeParallel, "test", 2},
		{ModeReview, "test", 2},
		{ModeDebate, "test", 3},
		{ModeMerge, "test", 3},
		{"", "test", 2},
	}

	for _, tt := range tests {
		specs := ExpandCollaboration(tt.mode, tt.goal)
		if len(specs) != tt.wantSpec {
			t.Errorf("mode=%s: expected %d specs, got %d", tt.mode, tt.wantSpec, len(specs))
		}
	}
}

func TestEngineCreation(t *testing.T) {
	runner := newTestRunner()
	cfg := &Config{Name: "test-team", Mode: ModeParallel}
	engine := NewEngine(cfg, runner, nil)
	if engine == nil {
		t.Fatal("expected engine, got nil")
	}

	collab := engine.CreateCollaboration("test goal")
	if collab == nil {
		t.Fatal("expected collaboration, got nil")
	}
	state := collab.State()
	if state.Goal != "test goal" {
		t.Errorf("expected goal='test goal', got '%s'", state.Goal)
	}
}

func TestValidateMode(t *testing.T) {
	valid := []string{"parallel", "review", "debate", "merge", "PARALLEL"}
	for _, m := range valid {
		if !ValidateMode(m) {
			t.Errorf("expected %s to be valid mode", m)
		}
	}
	invalid := []string{"invalid", "", "custom"}
	for _, m := range invalid {
		if ValidateMode(m) {
			t.Errorf("expected %s to be invalid mode", m)
		}
	}
}

func TestModeDescriptions(t *testing.T) {
	descs := ModeDescriptions()
	if len(descs) != len(ValidModes) {
		t.Errorf("expected %d mode descriptions, got %d", len(ValidModes), len(descs))
	}
	for _, m := range ValidModes {
		if _, ok := descs[m]; !ok {
			t.Errorf("missing description for mode %s", m)
		}
	}
}

func TestCollaborationEvent(t *testing.T) {
	ch := make(chan CollaborationEvent, 10)
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "event test", runner, nil)
	c.OnEvent(ch)

	c.emit(CollaborationEvent{Type: "start", Message: "test"})
	ev := <-ch
	if ev.Type != "start" {
		t.Errorf("expected type=start, got %s", ev.Type)
	}
	if ev.Message != "test" {
		t.Errorf("expected message=test, got %s", ev.Message)
	}
}

func TestRunPhase(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "run phase test", runner, nil)
	phase := c.state.Phases[0]
	result := c.runPhase(context.Background(), phase)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ID != phase.Spec.ID {
		t.Errorf("expected ID=%s, got %s", phase.Spec.ID, result.ID)
	}
}

func TestRunPhaseNilRunner(t *testing.T) {
	c := NewCollaboration(ModeParallel, "nil runner test", nil, nil)
	phase := c.state.Phases[0]
	result := c.runPhase(context.Background(), phase)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "error" {
		t.Errorf("expected status=error, got %s", result.Status)
	}
}

func TestCollectResults(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "collect test", runner, nil)
	c.state.Phases[0].Result = &subagent.Result{Output: "A"}
	c.state.Phases[1].Result = &subagent.Result{Output: "B"}

	results := c.collectResults()
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestBuildDebateMergePrompt(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeDebate, "debate test", runner, nil)
	c.state.Phases[0].Result = &subagent.Result{Output: "arg A"}
	c.state.Phases[1].Result = &subagent.Result{Output: "arg B"}

	prompt := c.buildDebateMergePrompt()
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if !containsStr(prompt, "arg A") || !containsStr(prompt, "arg B") {
		t.Error("prompt missing phase results")
	}
}

func TestBuildSynthesisPrompt(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeMerge, "merge test", runner, nil)
	workers := c.state.Phases[:2]
	workers[0].Result = &subagent.Result{Output: "fact 1"}
	workers[1].Result = &subagent.Result{Output: "fact 2"}

	prompt := c.buildSynthesisPrompt(workers)
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
}

func TestRunIdempotent(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "idempotent test", runner, nil)

	_, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	_, err = c.Run(context.Background())
	if err == nil {
		t.Error("expected error on second run")
	}
}

func TestFinalize(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "finalize test", runner, nil)

	state, err := c.finalize("completed", nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if state.Status != "completed" {
		t.Errorf("expected status=completed, got %s", state.Status)
	}
}

func TestFinalizeWithError(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "finalize error test", runner, nil)

	state, err := c.finalize("failed", context.DeadlineExceeded)
	if err != context.DeadlineExceeded {
		t.Errorf("expected deadline exceeded, got %v", err)
	}
	if state.Status != "failed" {
		t.Errorf("expected status=failed, got %s", state.Status)
	}
}

func TestResume(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "resume test", runner, nil)

	_, err := c.Resume(context.Background())
	if err == nil {
		t.Error("expected error when not paused")
	}

	c.mu.Lock()
	c.state.Status = "paused"
	c.mu.Unlock()

	_, err = c.Resume(context.Background())
	if err != nil {
		t.Errorf("expected no error on resume, got %v", err)
	}
}

func TestPhaseStateConstants(t *testing.T) {
	states := []PhaseState{PhasePending, PhaseRunning, PhaseFeedback, PhaseComplete, PhaseFailed, PhaseBlocked}
	for _, s := range states {
		if s == "" {
			t.Error("phase state should not be empty")
		}
	}
}

func TestCollaborationStateCopy(t *testing.T) {
	runner := newTestRunner()
	c := NewCollaboration(ModeParallel, "copy test", runner, nil)
	state1 := c.State()
	state2 := c.State()

	state1.Phases[0].ID = "modified"

	if state2.Phases[0].ID == "modified" {
		t.Error("State() should return a copy, not a reference")
	}
}

func TestEngineRun(t *testing.T) {
	runner := newTestRunner()
	cfg := &Config{Name: "run-test", Mode: ModeParallel}
	engine := NewEngine(cfg, runner, nil)

	state, err := engine.Run(context.Background(), "goal test")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if state.Goal != "goal test" {
		t.Errorf("expected goal='goal test', got '%s'", state.Goal)
	}
}

func TestEngineRunNilConfig(t *testing.T) {
	runner := newTestRunner()
	engine := NewEngine(nil, runner, nil)

	state, err := engine.Run(context.Background(), "default mode test")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if state.Mode != ModeParallel {
		t.Errorf("expected default mode=%s, got %s", ModeParallel, state.Mode)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}