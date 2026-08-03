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
	EventCompress   EventType = "compress"
	EventAnswer     EventType = "answer"
	EventError      EventType = "error"
	EventDone       EventType = "done"
	EventResume     EventType = "resume"
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
