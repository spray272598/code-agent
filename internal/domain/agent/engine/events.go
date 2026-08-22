package engine

import "time"

type EventType string

const (
	EventThought    EventType = "thought"
	EventTextDelta  EventType = "text_delta"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventPermission EventType = "permission"
	EventPlan       EventType = "plan"
	EventPlanUpdate EventType = "plan_update" // incremental plan progress (data: *plan.PlanView)
	EventReplan     EventType = "replan"      // plan rebuilt mid-run (data: *plan.Plan)
	EventCompress   EventType = "compress"
	EventAnswer     EventType = "answer"
	EventError      EventType = "error"
	EventDone       EventType = "done"
	EventResume     EventType = "resume"
	EventSkill      EventType = "skill"
	EventSlash      EventType = "slash"
	EventHook       EventType = "hook"
	EventReflect     EventType = "reflect"
	EventReview      EventType = "review"
	EventSubAgent    EventType = "subagent"
	EventObservation EventType = "observation"
	EventAction      EventType = "action"
	EventCheckpoint  EventType = "checkpoint"
	EventCancel      EventType = "cancel"
)

type Event struct {
	Type      EventType `json:"type"`
	SubType   string    `json:"subType,omitempty"`
	Step      int       `json:"step,omitempty"`
	Content   string    `json:"content,omitempty"`
	Data      any       `json:"data,omitempty"`
	Completed bool      `json:"completed,omitempty"`
	Timestamp int64     `json:"timestamp"`
}

func NewEvent(t EventType, step int, content string) *Event {
	return &Event{Type: t, Step: step, Content: content, Timestamp: time.Now().UnixMilli()}
}

type Result struct {
	SessionID      string
	Response       string
	Steps          int
	ToolCalls      int
	TokenUsed      int
	NeedPermission bool
	Pending        any
	ErrorClass     string
}

// ControlSignal is a mid-run instruction sent to the engine loop from the
// caller (e.g. user interrupt, request to re-plan, or resume after pause).
type ControlSignal int

const (
	// ControlNone is the zero value (no-op).
	ControlNone ControlSignal = iota
	// ControlReplan asks the loop to rebuild the plan from the current goal.
	ControlReplan
	// ControlReplanWithGoal asks the loop to rebuild the plan with a new goal.
	ControlReplanWithGoal
	// ControlPause asks the loop to stop at the next step boundary and emit a
	// checkpoint, awaiting resume.
	ControlPause
	// ControlResume resumes a paused loop.
	ControlResume
	// ControlInterrupt stops the loop immediately (equivalent to ctx cancel).
	ControlInterrupt
	// ControlPlanExplore enters the plan explore (read-only) phase: the guard
	// switches to readonly so the agent may inspect but not mutate.
	ControlPlanExplore
	// ControlPlanImplement exits the plan phase into the implement (writable)
	// phase: the guard returns to the configured workspace/strict tier.
	ControlPlanImplement
)

// Control is a single instruction delivered over a control channel.
type Control struct {
	Signal ControlSignal
	Goal   string // used by ControlReplanWithGoal
}
