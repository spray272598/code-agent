package workflow

type Outcome string

const (
	OutcomeCompleted     Outcome = "completed"
	OutcomePaused        Outcome = "paused"
	OutcomeBudgetExceeded Outcome = "budget_exceeded"
	OutcomeCancelled     Outcome = "cancelled"
	OutcomeFailed        Outcome = "failed"
)

type StepResult struct {
	Index   int            `json:"index"`
	Name    string         `json:"name"`
	Outcome Outcome        `json:"outcome"`
	Data    map[string]any `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type WorkflowSpec struct {
	ID     string     `json:"id"`
	Steps  []StepSpec `json:"steps"`
	Budget int        `json:"budget"`
}

type StepSpec struct {
	Index  int            `json:"index"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Prompt string         `json:"prompt,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}