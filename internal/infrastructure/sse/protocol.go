package sse

import (
	"encoding/json"
	"strconv"
	"time"
)

const (
	EventReasoningDelta EventType = "reasoning_delta"
	EventTextDelta      EventType = "text_delta"
	EventToolCallDelta  EventType = "tool_call_delta"
	EventToolResult     EventType = "tool_result"
	EventPlan           EventType = "plan"
	EventPlanUpdate     EventType = "plan_update"
	EventAnswer         EventType = "answer"
	EventDone           EventType = "done"
	EventError          EventType = "error"
	EventPermission     EventType = "permission"
	EventCheckpoint     EventType = "checkpoint"
	EventCancel         EventType = "cancel"
	EventSystem         EventType = "system"
	EventHeartbeat      EventType = "heartbeat"
)

type EventType string

type StructuredEvent struct {
	Type      EventType       `json:"type"`
	Seq       uint64          `json:"seq"`
	SessionID string          `json:"sessionId,omitempty"`
	Step      int             `json:"step,omitempty"`
	Delta     string          `json:"delta,omitempty"`
	Content   string          `json:"content,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Completed bool            `json:"completed,omitempty"`
	Reasoning *ReasoningMeta   `json:"reasoning,omitempty"`
	Usage     *TokenUsage     `json:"usage,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
	Timestamp int64           `json:"timestamp"`
}

type ReasoningMeta struct {
	Delta   string `json:"delta,omitempty"`
	TokenID int    `json:"tokenId,omitempty"`
	Phase   string `json:"phase,omitempty"`
}

type TokenUsage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

func (e *StructuredEvent) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

func (e *StructuredEvent) SSEEventName() string {
	return string(e.Type)
}

func (e *StructuredEvent) SSEEventID() string {
	if e.Seq > 0 {
		return strconv.FormatUint(e.Seq, 10)
	}
	return ""
}

func NewStructuredEvent(eventType EventType, sessionID string, seq uint64) *StructuredEvent {
	return &StructuredEvent{
		Type:      eventType,
		Seq:       seq,
		SessionID: sessionID,
		Timestamp: time.Now().UnixMilli(),
	}
}

func NewHeartbeatEvent(sessionID string, seq uint64) *StructuredEvent {
	return NewStructuredEvent(EventHeartbeat, sessionID, seq)
}

func NewSystemEvent(sessionID string, seq uint64, msg string) *StructuredEvent {
	ev := NewStructuredEvent(EventSystem, sessionID, seq)
	ev.Content = msg
	return ev
}

func NewErrorEvent(sessionID string, seq uint64, errMsg string) *StructuredEvent {
	ev := NewStructuredEvent(EventError, sessionID, seq)
	ev.Content = errMsg
	ev.Completed = true
	return ev
}

func NewDoneEvent(sessionID string, seq uint64) *StructuredEvent {
	ev := NewStructuredEvent(EventDone, sessionID, seq)
	ev.Completed = true
	return ev
}
