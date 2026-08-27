package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDAGEngineBasic(t *testing.T) {
	e := NewDAGEngine()
	e.Register("echo", func(ctx context.Context, step *DAGStep, state *DAGState) error {
		state.mu.Lock()
		state.Results[step.ID].Output = "done"
		state.mu.Unlock()
		return nil
	})

	wf := &DAGWorkflow{
		ID: "dag-1",
		Steps: []*DAGStep{
			{ID: "a", Action: "echo"},
			{ID: "b", Action: "echo", DependsOn: []string{"a"}},
		},
	}
	state, err := e.Execute(context.Background(), wf)
	if err != nil {
		t.Fatal(err)
	}
	if state.GetResult("a").Status != "complete" {
		t.Error("step a should be complete")
	}
	if state.GetResult("b").Status != "complete" {
		t.Error("step b should be complete")
	}
}

func TestDAGEngineParallel(t *testing.T) {
	e := NewDAGEngine()
	e.Register("slow", func(ctx context.Context, step *DAGStep, state *DAGState) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})

	wf := &DAGWorkflow{
		ID: "dag-parallel",
		Steps: []*DAGStep{
			{ID: "a", Action: "slow"},
			{ID: "b", Action: "slow"},
			{ID: "c", Action: "slow", DependsOn: []string{"a", "b"}},
		},
	}
	start := time.Now()
	_, err := e.Execute(context.Background(), wf)
	if err != nil {
		t.Fatal(err)
	}
	// a+b run in parallel (~20ms), c waits (~20ms) = ~40ms total
	if time.Since(start) > 100*time.Millisecond {
		t.Error("expected parallel execution")
	}
}

func TestDAGEngineFailure(t *testing.T) {
	e := NewDAGEngine()
	e.Register("fail", func(ctx context.Context, step *DAGStep, state *DAGState) error {
		return fmt.Errorf("boom")
	})

	wf := &DAGWorkflow{
		ID: "dag-fail",
		Steps: []*DAGStep{
			{ID: "a", Action: "fail"},
		},
	}
	_, err := e.Execute(context.Background(), wf)
	if err == nil {
		t.Error("expected error")
	}
}

func TestDAGEngineRetry(t *testing.T) {
	e := NewDAGEngine()
	attempts := 0
	e.Register("flaky", func(ctx context.Context, step *DAGStep, state *DAGState) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("retry %d", attempts)
		}
		return nil
	})

	wf := &DAGWorkflow{
		ID: "dag-retry",
		Steps: []*DAGStep{
			{ID: "a", Action: "flaky", RetryPolicy: &DAGRetry{MaxRetries: 3, Backoff: time.Millisecond}},
		},
	}
	state, err := e.Execute(context.Background(), wf)
	if err != nil {
		t.Fatal(err)
	}
	if state.GetResult("a").Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", state.GetResult("a").Attempts)
	}
}
