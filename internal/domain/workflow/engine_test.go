package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type stubHost struct {
	calls     atomic.Int64
	shouldErr bool
	pauseOn   *int
	callCount atomic.Int64
}

func (h *stubHost) Execute(ctx context.Context, req HostRequest) (HostResponse, error) {
	n := h.calls.Add(1) - 1
	if h.shouldErr {
		return HostResponse{}, fmt.Errorf("host error: %s", req.Kind)
	}
	if h.pauseOn != nil && int(n) == *h.pauseOn {
		return HostResponse{
			Result: map[string]any{"paused": true, "step": req.Payload["stepName"]},
			Paused: true,
		}, nil
	}
	return HostResponse{
		Result: map[string]any{
			"kind":  req.Kind,
			"step":  req.Payload["stepName"],
			"calls": h.callCount.Add(1),
		},
	}, nil
}

func intPtr(v int) *int { return &v }

func TestSequentialExecution(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-seq",
		Steps: []StepSpec{
			{Index: 0, Name: "step1", Type: "agent", Prompt: "first"},
			{Index: 1, Name: "step2", Type: "agent", Prompt: "second"},
			{Index: 2, Name: "step3", Type: "log", Params: map[string]any{"msg": "done"}},
		},
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(res.Steps))
	}
	for i, s := range res.Steps {
		if s.Outcome != OutcomeCompleted {
			t.Errorf("step %d: expected completed, got %s", i, s.Outcome)
		}
	}
	if res.Spent != 2 {
		t.Errorf("expected spent 2, got %d", res.Spent)
	}
}

func TestBudgetExceeded(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-budget",
		Steps: []StepSpec{
			{Index: 0, Name: "step1", Type: "agent", Prompt: "first"},
			{Index: 1, Name: "step2", Type: "agent", Prompt: "second"},
			{Index: 2, Name: "step3", Type: "agent", Prompt: "third"},
		},
		Budget: 2,
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeBudgetExceeded {
		t.Fatalf("expected budget_exceeded, got %s", res.Outcome)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(res.Steps))
	}
	if res.Steps[2].Outcome != OutcomeBudgetExceeded {
		t.Errorf("step 2: expected budget_exceeded, got %s", res.Steps[2].Outcome)
	}
	if res.Spent != 2 {
		t.Errorf("expected spent 2, got %d", res.Spent)
	}
}

func TestBudgetUnlimited(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-unlimited",
		Steps: []StepSpec{
			{Index: 0, Name: "s1", Type: "agent", Prompt: "a"},
			{Index: 1, Name: "s2", Type: "agent", Prompt: "b"},
			{Index: 2, Name: "s3", Type: "agent", Prompt: "c"},
		},
		Budget: 0,
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}
	if res.Spent != 3 {
		t.Errorf("expected spent 3, got %d", res.Spent)
	}
}

func TestParallelStepDelegation(t *testing.T) {
	var kindCalled atomic.Value
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-parallel",
		Steps: []StepSpec{
			{Index: 0, Name: "parallel-step", Type: "parallel", Prompt: "run in parallel"},
		},
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(res.Steps))
	}
	s := res.Steps[0]
	if s.Data["kind"] != "parallel_call" {
		t.Errorf("expected kind=parallel_call, got %v", s.Data["kind"])
	}
	kindCalled.Store(s.Data["kind"])
}

func TestCancellation(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	ctx, cancel := context.WithCancel(context.Background())

	spec := WorkflowSpec{
		ID: "test-cancel",
		Steps: []StepSpec{
			{Index: 0, Name: "step1", Type: "agent", Prompt: "first"},
			{Index: 1, Name: "step2", Type: "agent", Prompt: "second"},
			{Index: 2, Name: "step3", Type: "agent", Prompt: "third"},
		},
	}

	cancel()

	res, err := engine.Run(ctx, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("expected cancelled, got %s", res.Outcome)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step (only the cancelled entry), got %d", len(res.Steps))
	}
	if res.Steps[0].Outcome != OutcomeCancelled {
		t.Errorf("step outcome: expected cancelled, got %s", res.Steps[0].Outcome)
	}
}

func TestCancellationMidExecution(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	ctx, cancel := context.WithCancel(context.Background())

	spec := WorkflowSpec{
		ID: "test-cancel-mid",
		Steps: []StepSpec{
			{Index: 0, Name: "step1", Type: "agent", Prompt: "first"},
			{Index: 1, Name: "step2", Type: "agent", Prompt: "second"},
		},
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	res, err := engine.Run(ctx, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = res
}

func TestOutcomeFailed(t *testing.T) {
	host := &stubHost{shouldErr: true}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-fail",
		Steps: []StepSpec{
			{Index: 0, Name: "bad-step", Type: "agent", Prompt: "will fail"},
		},
	}

	res, err := engine.Run(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if res.Outcome != OutcomeFailed {
		t.Fatalf("expected failed, got %s", res.Outcome)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(res.Steps))
	}
	if res.Steps[0].Outcome != OutcomeFailed {
		t.Errorf("step: expected failed, got %s", res.Steps[0].Outcome)
	}
}

func TestOutcomePaused(t *testing.T) {
	host := &stubHost{pauseOn: intPtr(0)}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-pause",
		Steps: []StepSpec{
			{Index: 0, Name: "await", Type: "await_user", Prompt: "waiting"},
			{Index: 1, Name: "after", Type: "agent", Prompt: "should not run"},
		},
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomePaused {
		t.Fatalf("expected paused, got %s", res.Outcome)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step (paused), got %d", len(res.Steps))
	}
	if res.Steps[0].Outcome != OutcomePaused {
		t.Errorf("step: expected paused, got %s", res.Steps[0].Outcome)
	}
}

func TestJournalReplay(t *testing.T) {
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "journal.json")

	host := &stubHost{}

	j, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	spec := WorkflowSpec{
		ID: "test-replay",
		Steps: []StepSpec{
			{Index: 0, Name: "s1", Type: "agent", Prompt: "step one"},
			{Index: 1, Name: "s2", Type: "agent", Prompt: "step two"},
		},
	}

	engine := NewEngine(host, j)
	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("first run: expected completed, got %s", res.Outcome)
	}
	if host.calls.Load() != 2 {
		t.Fatalf("first run: expected 2 host calls, got %d", host.calls.Load())
	}

	if err := j.Save(); err != nil {
		t.Fatalf("journal save: %v", err)
	}

	j2, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("reload journal: %v", err)
	}

	if j2.Len() != 2 {
		t.Fatalf("reloaded journal: expected 2 entries, got %d", j2.Len())
	}

	engine2 := NewEngine(host, j2)
	res2, err := engine2.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res2.Outcome != OutcomeCompleted {
		t.Fatalf("second run: expected completed, got %s", res2.Outcome)
	}
	if host.calls.Load() != 2 {
		t.Errorf("replay should not re-exec steps, expected 2 total calls, got %d", host.calls.Load())
	}

	_ = os.RemoveAll(dir)
}

func TestJournalReplayPartial(t *testing.T) {
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "journal.json")

	host := &stubHost{}

	j, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	spec := WorkflowSpec{
		ID: "test-partial",
		Steps: []StepSpec{
			{Index: 0, Name: "s1", Type: "agent", Prompt: "step one"},
			{Index: 1, Name: "s2", Type: "agent", Prompt: "step two"},
			{Index: 2, Name: "s3", Type: "agent", Prompt: "step three"},
		},
	}

	engine := NewEngine(host, j)
	res, err := engine.Run(context.Background(), WorkflowSpec{
		ID: "test-partial",
		Steps: []StepSpec{
			spec.Steps[0], spec.Steps[1],
		},
	})
	if err != nil {
		t.Fatalf("partial run: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("partial run: expected completed, got %s", res.Outcome)
	}
	if host.calls.Load() != 2 {
		t.Fatalf("partial run: expected 2 calls, got %d", host.calls.Load())
	}

	if err := j.Save(); err != nil {
		t.Fatalf("journal save: %v", err)
	}

	j2, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("reload journal: %v", err)
	}

	engine2 := NewEngine(host, j2)
	res2, err := engine2.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if res2.Outcome != OutcomeCompleted {
		t.Fatalf("resume run: expected completed, got %s", res2.Outcome)
	}
	if host.calls.Load() != 3 {
		t.Errorf("resume: expected 3 total calls (2 replayed + 1 new), got %d", host.calls.Load())
	}

	_ = os.RemoveAll(dir)
}

func TestJournalIdempotency(t *testing.T) {
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "journal.json")

	j, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	payload := map[string]any{"key": "value"}
	hash := ComputeHash("agent_call", payload)

	result := map[string]any{"data": "result1"}
	if err := j.Record(5, "agent_call", hash, result); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, ok := j.Replay(5, "agent_call", hash)
	if !ok {
		t.Fatal("expected replay hit")
	}
	if got["data"] != "result1" {
		t.Errorf("expected result1, got %v", got["data"])
	}

	_, ok = j.Replay(5, "agent_call", "wronghash")
	if ok {
		t.Error("expected replay miss for wrong hash")
	}

	_, ok = j.Replay(5, "parallel_call", hash)
	if ok {
		t.Error("expected replay miss for wrong kind")
	}

	_, ok = j.Replay(99, "agent_call", hash)
	if ok {
		t.Error("expected replay miss for wrong seq")
	}
}

func TestJournalPersistence(t *testing.T) {
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "journal.json")

	j, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	payload := map[string]any{"x": 1}
	hash := ComputeHash("agent_call", payload)
	if err := j.Record(0, "agent_call", hash, map[string]any{"v": "one"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := j.Record(1, "parallel_call", ComputeHash("parallel_call", map[string]any{"y": 2}), map[string]any{"v": "two"}); err != nil {
		t.Fatalf("record 2: %v", err)
	}

	if err := j.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	j2, err := NewJournal(journalPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if j2.Len() != 2 {
		t.Fatalf("expected 2 entries after reload, got %d", j2.Len())
	}

	got, ok := j2.Replay(0, "agent_call", hash)
	if !ok {
		t.Fatal("expected replay hit after reload")
	}
	if got["v"] != "one" {
		t.Errorf("expected v=one, got %v", got["v"])
	}
}

func TestJournalEmptyPath(t *testing.T) {
	j, err := NewJournal("")
	if err != nil {
		t.Fatalf("new journal empty path: %v", err)
	}
	if j.Len() != 0 {
		t.Errorf("expected 0 entries, got %d", j.Len())
	}
	if err := j.Save(); err != nil {
		t.Errorf("save empty path: %v", err)
	}
}

func TestComputeHash(t *testing.T) {
	payload := map[string]any{"a": 1, "b": "two"}
	h1 := ComputeHash("agent_call", payload)
	h2 := ComputeHash("agent_call", payload)
	if h1 != h2 {
		t.Errorf("same inputs should produce same hash: %s vs %s", h1, h2)
	}
	h3 := ComputeHash("parallel_call", payload)
	if h1 == h3 {
		t.Error("different kinds should produce different hashes")
	}
	payload2 := map[string]any{"a": 1, "b": "three"}
	h4 := ComputeHash("agent_call", payload2)
	if h1 == h4 {
		t.Error("different payloads should produce different hashes")
	}
}

func TestAwaitUserStep(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-await",
		Steps: []StepSpec{
			{Index: 0, Name: "wait", Type: "await_user", Prompt: "please confirm"},
		},
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed for non-paused await, got %s", res.Outcome)
	}
}

func TestEmptyWorkflow(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{ID: "empty"}
	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}
	if len(res.Steps) != 0 {
		t.Fatalf("expected 0 steps, got %d", len(res.Steps))
	}
}

func TestLogStep(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-log",
		Steps: []StepSpec{
			{Index: 0, Name: "log-step", Type: "log", Params: map[string]any{"msg": "hello"}},
		},
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}
	if res.Spent != 0 {
		t.Errorf("log steps should not consume budget, spent=%d", res.Spent)
	}
	if res.Steps[0].Data["msg"] != "hello" {
		t.Errorf("expected msg=hello, got %v", res.Steps[0].Data["msg"])
	}
}

func TestBudgetWithLogSteps(t *testing.T) {
	host := &stubHost{}
	engine := NewEngine(host, nil)

	spec := WorkflowSpec{
		ID: "test-budget-log",
		Steps: []StepSpec{
			{Index: 0, Name: "log1", Type: "log", Params: map[string]any{"msg": "skip"}},
			{Index: 1, Name: "agent1", Type: "agent", Prompt: "run"},
			{Index: 2, Name: "log2", Type: "log", Params: map[string]any{"msg": "skip"}},
			{Index: 3, Name: "agent2", Type: "agent", Prompt: "run2"},
		},
		Budget: 2,
	}

	res, err := engine.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != OutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}
	if res.Spent != 2 {
		t.Errorf("expected spent 2, got %d", res.Spent)
	}
}