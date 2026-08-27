package workflow

import (
	"context"
	"fmt"
	"time"
)

// SequentialEngine executes WorkflowSpec steps one by one, delegating to a Host.
// This is the original engine used by LoopBridge.
type SequentialEngine struct {
	host Host
}

// NewSequentialEngine creates a sequential workflow engine.
func NewSequentialEngine(host Host) *SequentialEngine {
	return &SequentialEngine{host: host}
}

// Run executes a WorkflowSpec step by step.
func (e *SequentialEngine) Run(ctx context.Context, spec WorkflowSpec) (*RunResult, error) {
	result := &RunResult{
		WorkflowID: spec.ID,
		Outcome:    OutcomeCompleted,
		Results:    make([]StepResult, 0, len(spec.Steps)),
	}

	for _, step := range spec.Steps {
		if ctx.Err() != nil {
			result.Outcome = OutcomeCancelled
			return result, ctx.Err()
		}

		stepResult := StepResult{
			Index:   step.Index,
			Name:    step.Name,
			Outcome: OutcomeCompleted,
		}

		if e.host != nil {
			req := HostRequest{
				Kind: step.Type,
				Payload: map[string]any{
					"stepName": step.Name,
					"prompt":   step.Prompt,
					"params":   step.Params,
				},
			}
			resp, err := e.host.Execute(ctx, req)
			if err != nil {
				stepResult.Outcome = OutcomeFailed
				stepResult.Error = err.Error()
				result.Outcome = OutcomeFailed
				result.Results = append(result.Results, stepResult)
				return result, fmt.Errorf("step %s failed: %w", step.Name, err)
			}
			if resp.Paused {
				stepResult.Outcome = OutcomePaused
				result.Outcome = OutcomePaused
				result.Results = append(result.Results, stepResult)
				return result, nil
			}
			stepResult.Data = resp.Result
		}

		result.Results = append(result.Results, stepResult)
		_ = time.Now() // prevent unused import
	}

	return result, nil
}
