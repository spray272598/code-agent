package subagent

import "time"

// SubagentType identifies the role/persona of a subagent.
type SubagentType string

const (
	SubagentTypeGeneralPurpose SubagentType = "general-purpose"
	SubagentTypeExplore        SubagentType = "explore"
	SubagentTypePlan           SubagentType = "plan"
	SubagentTypeVerify         SubagentType = "verify"
	SubagentTypeStrategist     SubagentType = "strategist"
)

// IsolationMode defines how a subagent's workspace is isolated.
type IsolationMode string

const (
	IsolationNone    IsolationMode = "none"
	IsolationWorktree IsolationMode = "worktree"
)

// SubagentStatus identifies the lifecycle state of a subagent task.
type SubagentStatus int

const (
	SubagentStatusPending SubagentStatus = iota
	SubagentStatusRunning
	SubagentStatusCompleted
	SubagentStatusFailed
	SubagentStatusCancelled
)

func (s SubagentStatus) String() string {
	switch s {
	case SubagentStatusPending:
		return "pending"
	case SubagentStatusRunning:
		return "running"
	case SubagentStatusCompleted:
		return "completed"
	case SubagentStatusFailed:
		return "failed"
	case SubagentStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func (s SubagentStatus) IsTerminal() bool {
	switch s {
	case SubagentStatusCompleted, SubagentStatusFailed, SubagentStatusCancelled:
		return true
	default:
		return false
	}
}

// SubagentInput defines the input for spawning a subagent.
type SubagentInput struct {
	Prompt        string            `json:"prompt"`
	Description   string            `json:"description"`
	SubagentType  SubagentType      `json:"subagent_type"`
	Background    bool              `json:"background"`
	Isolation     IsolationMode     `json:"isolation,omitempty"`
	ResumeFrom    string            `json:"resume_from,omitempty"`
	CWD           string            `json:"cwd,omitempty"`
	Model         string            `json:"model,omitempty"`
	TaskID        string            `json:"task_id,omitempty"`
	ParentSession string            `json:"parent_session,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Validate checks input consistency.
func (in *SubagentInput) Validate() error {
	if in.Prompt == "" {
		return ErrEmptyPrompt
	}
	if in.SubagentType == "" {
		in.SubagentType = SubagentTypeGeneralPurpose
	}
	if in.Isolation == "" {
		in.Isolation = IsolationNone
	}
	if in.Isolation == IsolationWorktree && in.CWD != "" {
		return ErrConflictingIsolation
	}
	return nil
}

// SubagentOutput is the completed output from a subagent.
type SubagentOutput struct {
	TaskID      string        `json:"task_id"`
	Status      SubagentStatus `json:"status"`
	Result      string        `json:"result,omitempty"`
	Error       string        `json:"error,omitempty"`
	TokensUsed  int64         `json:"tokens_used"`
	DurationMs  int64         `json:"duration_ms"`
	Model       string        `json:"model,omitempty"`
	CompletedAt time.Time     `json:"completed_at"`
}

// SubagentHandle allows tracking a running subagent.
type SubagentHandle struct {
	TaskID       string         `json:"task_id"`
	Status       SubagentStatus `json:"status"`
	Description  string         `json:"description"`
	SubagentType SubagentType   `json:"subagent_type"`
	StartedAt    time.Time      `json:"started_at"`
	ParentSession string        `json:"parent_session,omitempty"`
}

// SubagentProgress is a live progress update from a running subagent.
type SubagentProgress struct {
	TaskID        string `json:"task_id"`
	TokensUsed    int64  `json:"tokens_used"`
	LiveTokens    int64  `json:"live_tokens"`
	FinishedTokens int64 `json:"finished_tokens"`
	Step          int    `json:"step"`
	Message       string `json:"message,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// SubagentTypeInfo describes a subagent type for tool schema generation.
type SubagentTypeInfo struct {
	Type        SubagentType   `json:"type"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Capabilities []string      `json:"capabilities"`
	Tools       []string       `json:"tools"`
	Builtin     bool           `json:"builtin"`
}

// BuiltinSubagentTypes returns the built-in subagent type definitions.
func BuiltinSubagentTypes() []SubagentTypeInfo {
	return []SubagentTypeInfo{
		{
			Type:        SubagentTypeGeneralPurpose,
			Label:       "General Purpose",
			Description: "Balanced subagent for coding, research, and analysis tasks",
			Capabilities: []string{"code", "research", "analysis", "file_operations"},
			Tools:       []string{"read_file", "write_file", "edit_file", "bash", "grep", "list_dir"},
			Builtin:     true,
		},
		{
			Type:        SubagentTypeExplore,
			Label:       "Explore",
			Description: "Specialized for codebase exploration, search, and understanding",
			Capabilities: []string{"search", "navigation", "reading", "analysis"},
			Tools:       []string{"read_file", "grep", "list_dir", "search_code"},
			Builtin:     true,
		},
		{
			Type:        SubagentTypePlan,
			Label:       "Plan",
			Description: "Specialized for task decomposition and planning",
			Capabilities: []string{"planning", "analysis", "task_decomposition"},
			Tools:       []string{"read_file", "grep", "list_dir"},
			Builtin:     true,
		},
		{
			Type:        SubagentTypeVerify,
			Label:       "Verify",
			Description: "Specialized for verification and validation of deliverables",
			Capabilities: []string{"verification", "testing", "review"},
			Tools:       []string{"read_file", "bash", "grep", "list_dir"},
			Builtin:     true,
		},
		{
			Type:        SubagentTypeStrategist,
			Label:       "Strategist",
			Description: "Specialized for restructuring stalled goals",
			Capabilities: []string{"analysis", "restructuring", "root_cause"},
			Tools:       []string{"read_file", "grep", "list_dir"},
			Builtin:     true,
		},
	}
}

// ResolveSubagentType returns info for a given subagent type.
func ResolveSubagentType(st SubagentType) (SubagentTypeInfo, bool) {
	for _, info := range BuiltinSubagentTypes() {
		if info.Type == st {
			return info, true
		}
	}
	return SubagentTypeInfo{}, false
}
