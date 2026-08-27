package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type LoopBridge struct {
	mu           sync.RWMutex
	loopExecutor LoopExecutor
	engine       *SequentialEngine
	activeRuns   map[string]*ActiveRun
}

type LoopExecutor interface {
	ExecuteAgentStep(ctx context.Context, stepName, prompt string, params map[string]any) (map[string]any, error)
	ExecuteParallelStep(ctx context.Context, stepName string, prompts []string, params map[string]any) ([]map[string]any, error)
	AwaitUserInput(ctx context.Context, stepName string, prompt string) (string, error)
}

type ActiveRun struct {
	Spec      WorkflowSpec
	StartedAt time.Time
	Completed bool
	Active    bool
	Result    *RunResult
	err       error
	cancel    context.CancelFunc
}

func NewLoopBridge(executor LoopExecutor) *LoopBridge {
	return &LoopBridge{
		loopExecutor: executor,
		engine:       NewSequentialEngine(nil),
		activeRuns:   make(map[string]*ActiveRun),
	}
}

func (b *LoopBridge) RunWorkflow(ctx context.Context, spec WorkflowSpec) (*RunResult, error) {
	ctx, cancel := context.WithCancel(ctx)

	run := &ActiveRun{
		Spec:      spec,
		StartedAt: time.Now(),
		Active:    true,
		cancel:    cancel,
	}

	b.mu.Lock()
	b.activeRuns[spec.ID] = run
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.activeRuns, spec.ID)
		b.mu.Unlock()
		cancel()
	}()

	engine := NewSequentialEngine(b.buildHost(ctx))
	result, err := engine.Run(ctx, spec)

	b.mu.Lock()
	run.Completed = true
	run.Result = result
	run.err = err
	b.mu.Unlock()

	return result, err
}

func (b *LoopBridge) buildHost(ctx context.Context) Host {
	return &loopHost{bridge: b, ctx: ctx}
}

type loopHost struct {
	bridge *LoopBridge
	ctx    context.Context
}

func (h *loopHost) Execute(ctx context.Context, req HostRequest) (HostResponse, error) {
	exec := h.bridge.loopExecutor
	if exec == nil {
		return HostResponse{}, fmt.Errorf("no loop executor configured")
	}

	switch req.Kind {
	case "agent_call":
		prompt, _ := req.Payload["prompt"].(string)
		params, _ := req.Payload["params"].(map[string]any)
		stepName, _ := req.Payload["stepName"].(string)

		result, err := exec.ExecuteAgentStep(ctx, stepName, prompt, params)
		if err != nil {
			return HostResponse{Error: err}, err
		}
		return HostResponse{Result: result}, nil

	case "parallel_call":
		prompt, _ := req.Payload["prompt"].(string)
		params, _ := req.Payload["params"].(map[string]any)
		stepName, _ := req.Payload["stepName"].(string)

		prompts := []string{prompt}
		if pList, ok := req.Payload["prompts"].([]string); ok {
			prompts = pList
		}

		results, err := exec.ExecuteParallelStep(ctx, stepName, prompts, params)
		if err != nil {
			return HostResponse{Error: err}, err
		}
		merged := make(map[string]any)
		for i, r := range results {
			for k, v := range r {
				merged[fmt.Sprintf("step%d_%s", i, k)] = v
			}
		}
		return HostResponse{Result: merged}, nil

	case "await_user":
		prompt, _ := req.Payload["prompt"].(string)
		stepName, _ := req.Payload["stepName"].(string)

		userInput, err := exec.AwaitUserInput(ctx, stepName, prompt)
		if err != nil {
			return HostResponse{Paused: true}, nil
		}
		return HostResponse{
			Result: map[string]any{"userInput": userInput},
		}, nil

	default:
		return HostResponse{Result: req.Payload}, nil
	}
}

func (b *LoopBridge) CancelRun(runID string) error {
	b.mu.Lock()
	run, ok := b.activeRuns[runID]
	b.mu.Unlock()

	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}

	run.cancel()
	return nil
}

func (b *LoopBridge) GetActiveRun(runID string) (*ActiveRun, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	run, ok := b.activeRuns[runID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	return run, nil
}

func (b *LoopBridge) ListActiveRuns() []*ActiveRun {
	b.mu.RLock()
	defer b.mu.RUnlock()

	runs := make([]*ActiveRun, 0, len(b.activeRuns))
	for _, r := range b.activeRuns {
		runs = append(runs, r)
	}
	return runs
}

func (b *LoopBridge) PauseRun(runID string) error {
	return b.CancelRun(runID)
}

type WorkflowBuilder struct {
	spec WorkflowSpec
}

func NewWorkflowBuilder(id string) *WorkflowBuilder {
	return &WorkflowBuilder{
		spec: WorkflowSpec{
			ID:    id,
			Steps: []StepSpec{},
		},
	}
}

func (b *WorkflowBuilder) WithBudget(budget int) *WorkflowBuilder {
	b.spec.Budget = budget
	return b
}

func (b *WorkflowBuilder) AddAgentStep(name, prompt string, params map[string]any) *WorkflowBuilder {
	b.spec.Steps = append(b.spec.Steps, StepSpec{
		Index:  len(b.spec.Steps),
		Name:   name,
		Type:   "agent",
		Prompt: prompt,
		Params: params,
	})
	return b
}

func (b *WorkflowBuilder) AddParallelStep(name string, prompts []string, params map[string]any) *WorkflowBuilder {
	b.spec.Steps = append(b.spec.Steps, StepSpec{
		Index:  len(b.spec.Steps),
		Name:   name,
		Type:   "parallel",
		Prompt: prompts[0],
		Params: params,
	})
	return b
}

func (b *WorkflowBuilder) AddAwaitUserStep(name, prompt string) *WorkflowBuilder {
	b.spec.Steps = append(b.spec.Steps, StepSpec{
		Index:  len(b.spec.Steps),
		Name:   name,
		Type:   "await_user",
		Prompt: prompt,
	})
	return b
}

func (b *WorkflowBuilder) AddLogStep(name string, params map[string]any) *WorkflowBuilder {
	b.spec.Steps = append(b.spec.Steps, StepSpec{
		Index:  len(b.spec.Steps),
		Name:   name,
		Type:   "log",
		Params: params,
	})
	return b
}

func (b *WorkflowBuilder) Build() WorkflowSpec {
	return b.spec
}
