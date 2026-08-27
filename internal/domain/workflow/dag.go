package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DAGEngine executes workflows as directed acyclic graphs with parallel
// execution of independent steps. This extends the existing sequential
// WorkflowEngine with dependency-based scheduling.
type DAGEngine struct {
	mu      sync.RWMutex
	plugins map[string]DAGStepExecutor
}

// DAGStepExecutor executes a single DAG step.
type DAGStepExecutor func(ctx context.Context, step *DAGStep, state *DAGState) error

// DAGWorkflow defines a workflow with explicit dependencies.
type DAGWorkflow struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Steps []*DAGStep `json:"steps"`
}

// DAGStep is a single unit of work with dependencies.
type DAGStep struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Action      string         `json:"action"`
	Args        map[string]any `json:"args,omitempty"`
	DependsOn   []string       `json:"depends_on,omitempty"`
	RetryPolicy *DAGRetry      `json:"retry_policy,omitempty"`
	Timeout     time.Duration  `json:"timeout,omitempty"`
}

// DAGRetry defines retry behavior.
type DAGRetry struct {
	MaxRetries int           `json:"max_retries"`
	Backoff    time.Duration `json:"backoff"`
}

// DAGState tracks execution state.
type DAGState struct {
	mu       sync.RWMutex
	Results  map[string]*DAGStepResult `json:"results"`
	Workflow *DAGWorkflow              `json:"-"`
}

// DAGStepResult is the outcome of a step.
type DAGStepResult struct {
	StepID   string        `json:"stepId"`
	Status   string        `json:"status"`
	Output   string        `json:"output,omitempty"`
	Error    string        `json:"error,omitempty"`
	Attempts int           `json:"attempts"`
	Duration time.Duration `json:"duration"`
}

// NewDAGEngine creates a new DAG workflow engine.
func NewDAGEngine() *DAGEngine {
	return &DAGEngine{plugins: make(map[string]DAGStepExecutor)}
}

// Register adds a step executor.
func (e *DAGEngine) Register(action string, executor DAGStepExecutor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plugins[action] = executor
}

// Execute runs a DAG workflow to completion.
func (e *DAGEngine) Execute(ctx context.Context, wf *DAGWorkflow) (*DAGState, error) {
	state := &DAGState{
		Results:  make(map[string]*DAGStepResult),
		Workflow: wf,
	}
	for _, step := range wf.Steps {
		state.Results[step.ID] = &DAGStepResult{StepID: step.ID, Status: "pending"}
	}

	for {
		ready := e.findReady(wf, state)
		if len(ready) == 0 {
			break
		}
		var wg sync.WaitGroup
		for _, step := range ready {
			wg.Add(1)
			go func(s *DAGStep) {
				defer wg.Done()
				e.executeStep(ctx, s, state)
			}(step)
		}
		wg.Wait()

		state.mu.RLock()
		for _, result := range state.Results {
			if result.Status == "failed" {
				state.mu.RUnlock()
				return state, fmt.Errorf("workflow failed at step %s: %s", result.StepID, result.Error)
			}
		}
		state.mu.RUnlock()
	}
	return state, nil
}

func (e *DAGEngine) findReady(wf *DAGWorkflow, state *DAGState) []*DAGStep {
	state.mu.RLock()
	defer state.mu.RUnlock()
	var ready []*DAGStep
	for _, step := range wf.Steps {
		if state.Results[step.ID].Status != "pending" {
			continue
		}
		ok := true
		for _, dep := range step.DependsOn {
			dr := state.Results[dep]
			if dr == nil || dr.Status != "complete" {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, step)
		}
	}
	return ready
}

func (e *DAGEngine) executeStep(ctx context.Context, step *DAGStep, state *DAGState) {
	state.mu.Lock()
	state.Results[step.ID].Status = "running"
	state.mu.Unlock()

	e.mu.RLock()
	executor, ok := e.plugins[step.Action]
	e.mu.RUnlock()
	if !ok {
		state.mu.Lock()
		r := state.Results[step.ID]
		r.Status = "failed"
		r.Error = fmt.Sprintf("no executor for %q", step.Action)
		state.mu.Unlock()
		return
	}

	maxRetries := 1
	if step.RetryPolicy != nil && step.RetryPolicy.MaxRetries > 0 {
		maxRetries = step.RetryPolicy.MaxRetries + 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}
		lastErr = executor(ctx, step, state)
		if lastErr == nil {
			state.mu.Lock()
			r := state.Results[step.ID]
			r.Status = "complete"
			r.Attempts = attempt
			state.mu.Unlock()
			return
		}
		if attempt < maxRetries && step.RetryPolicy != nil {
			time.Sleep(step.RetryPolicy.Backoff)
		}
	}
	state.mu.Lock()
	r := state.Results[step.ID]
	r.Status = "failed"
	r.Error = lastErr.Error()
	r.Attempts = maxRetries
	state.mu.Unlock()
}

// GetResult returns a step's result.
func (s *DAGState) GetResult(stepID string) *DAGStepResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Results[stepID]
}
