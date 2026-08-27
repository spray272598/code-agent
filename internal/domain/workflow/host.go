package workflow

import "context"

type HostRequest struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

type HostResponse struct {
	Result map[string]any `json:"result,omitempty"`
	Paused bool           `json:"paused,omitempty"`
	Error  error          `json:"-"`
}

type Host interface {
	Execute(ctx context.Context, req HostRequest) (HostResponse, error)
}

// RunResult is the outcome of a workflow execution.
type RunResult struct {
	WorkflowID string         `json:"workflowId"`
	Outcome    Outcome        `json:"outcome"`
	Results    []StepResult   `json:"results"`
	Data       map[string]any `json:"data,omitempty"`
	Error      string         `json:"error,omitempty"`
}
