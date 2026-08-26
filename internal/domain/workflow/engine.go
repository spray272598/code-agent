package workflow

import (
	"context"
	"fmt"
)

type Engine struct {
	host    Host
	journal *Journal
}

func NewEngine(host Host, journal *Journal) *Engine {
	return &Engine{host: host, journal: journal}
}

type RunResult struct {
	Outcome Outcome      `json:"outcome"`
	Steps   []StepResult `json:"steps"`
	Spent   int          `json:"spent"`
}

func (e *Engine) Run(ctx context.Context, spec WorkflowSpec) (*RunResult, error) {
	if len(spec.Steps) == 0 {
		return &RunResult{Outcome: OutcomeCompleted}, nil
	}

	result := &RunResult{
		Outcome: OutcomeCompleted,
		Steps:   make([]StepResult, 0, len(spec.Steps)),
	}

	for i, step := range spec.Steps {
		select {
		case <-ctx.Done():
			result.Outcome = OutcomeCancelled
			result.Steps = append(result.Steps, StepResult{
				Index:   i,
				Name:    step.Name,
				Outcome: OutcomeCancelled,
				Error:   ctx.Err().Error(),
			})
			return result, nil
		default:
		}

		stepResult := e.executeStep(ctx, i, step, spec.Budget, result)
		result.Steps = append(result.Steps, stepResult)

		if stepResult.Outcome == OutcomeBudgetExceeded {
			result.Outcome = OutcomeBudgetExceeded
			return result, nil
		}
		if stepResult.Outcome == OutcomePaused {
			result.Outcome = OutcomePaused
			return result, nil
		}
		if stepResult.Outcome == OutcomeFailed {
			result.Outcome = OutcomeFailed
			return result, fmt.Errorf("step %q failed: %s", step.Name, stepResult.Error)
		}
	}

	return result, nil
}

func (e *Engine) executeStep(ctx context.Context, idx int, step StepSpec, budget int, result *RunResult) StepResult {
	switch step.Type {
	case "agent":
		return e.handleAgent(ctx, idx, step, budget, result)
	case "parallel":
		return e.handleParallel(ctx, idx, step, budget, result)
	case "await_user":
		return e.handleAwaitUser(ctx, idx, step)
	case "log":
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomeCompleted,
			Data:    step.Params,
		}
	default:
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomeCompleted,
			Data:    step.Params,
		}
	}
}

func (e *Engine) handleAgent(ctx context.Context, idx int, step StepSpec, budget int, result *RunResult) StepResult {
	req := HostRequest{
		Kind: "agent_call",
		Payload: map[string]any{
			"stepIndex": idx,
			"stepName":  step.Name,
			"prompt":    step.Prompt,
			"params":    step.Params,
		},
	}
	return e.executeHostCall(ctx, idx, step, req, budget, result)
}

func (e *Engine) handleParallel(ctx context.Context, idx int, step StepSpec, budget int, result *RunResult) StepResult {
	req := HostRequest{
		Kind: "parallel_call",
		Payload: map[string]any{
			"stepIndex": idx,
			"stepName":  step.Name,
			"prompt":    step.Prompt,
			"params":    step.Params,
		},
	}
	return e.executeHostCall(ctx, idx, step, req, budget, result)
}

func (e *Engine) handleAwaitUser(ctx context.Context, idx int, step StepSpec) StepResult {
	req := HostRequest{
		Kind: "await_user",
		Payload: map[string]any{
			"stepIndex": idx,
			"stepName":  step.Name,
			"prompt":    step.Prompt,
			"params":    step.Params,
		},
	}

	if e.journal != nil {
		reqHash := ComputeHash(req.Kind, req.Payload)
		if cached, ok := e.journal.Replay(uint64(idx), req.Kind, reqHash); ok {
			return StepResult{
				Index:   idx,
				Name:    step.Name,
				Outcome: OutcomeCompleted,
				Data:    cached,
			}
		}
	}

	resp, err := e.host.Execute(ctx, req)
	if err != nil {
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomeFailed,
			Error:   err.Error(),
		}
	}

	if resp.Paused {
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomePaused,
			Data:    resp.Result,
		}
	}

	if e.journal != nil {
		reqHash := ComputeHash(req.Kind, req.Payload)
		_ = e.journal.Record(uint64(idx), req.Kind, reqHash, resp.Result)
	}

	return StepResult{
		Index:   idx,
		Name:    step.Name,
		Outcome: OutcomeCompleted,
		Data:    resp.Result,
	}
}

func (e *Engine) executeHostCall(ctx context.Context, idx int, step StepSpec, req HostRequest, budget int, result *RunResult) StepResult {
	if budget > 0 && result.Spent >= budget {
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomeBudgetExceeded,
			Error:   fmt.Sprintf("budget %d exceeded (spent %d)", budget, result.Spent),
		}
	}

	if e.journal != nil {
		reqHash := ComputeHash(req.Kind, req.Payload)
		if cached, ok := e.journal.Replay(uint64(idx), req.Kind, reqHash); ok {
			result.Spent++
			return StepResult{
				Index:   idx,
				Name:    step.Name,
				Outcome: OutcomeCompleted,
				Data:    cached,
			}
		}
	}

	resp, err := e.host.Execute(ctx, req)
	if err != nil {
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomeFailed,
			Error:   err.Error(),
		}
	}

	if resp.Paused {
		return StepResult{
			Index:   idx,
			Name:    step.Name,
			Outcome: OutcomePaused,
			Data:    resp.Result,
		}
	}

	result.Spent++

	if e.journal != nil {
		reqHash := ComputeHash(req.Kind, req.Payload)
		_ = e.journal.Record(uint64(idx), req.Kind, reqHash, resp.Result)
	}

	return StepResult{
		Index:   idx,
		Name:    step.Name,
		Outcome: OutcomeCompleted,
		Data:    resp.Result,
	}
}
