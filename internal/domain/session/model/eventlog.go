package model

import (
	"fmt"
	"sync"
	"time"
)

// EventType classifies session events.
type EventType string

const (
	EventSessionCreated   EventType = "session.created"
	EventSessionForked    EventType = "session.forked"
	EventUserMessage      EventType = "user.message"
	EventAssistantMessage EventType = "assistant.message"
	EventToolCall         EventType = "tool.call"
	EventToolResult       EventType = "tool.result"
	EventPermissionAsk    EventType = "permission.ask"
	EventPermissionGrant  EventType = "permission.grant"
	EventPermissionDeny   EventType = "permission.deny"
	EventSummaryCreated   EventType = "summary.created"
)

// Event is a single immutable entry in the event log.
type Event struct {
	ID        string         `json:"id"`
	Seq       int            `json:"seq"`
	Type      EventType      `json:"type"`
	SessionID string         `json:"sessionId"`
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// EventLog is an append-only event stream for a session.
// Thread-safe. Events are immutable once appended.
type EventLog struct {
	mu      sync.RWMutex
	events  []Event
	nextSeq int
}

// NewEventLog creates an empty event log.
func NewEventLog() *EventLog {
	return &EventLog{nextSeq: 1}
}

// Append adds an event to the log. Returns the assigned sequence number.
func (l *EventLog) Append(id, sessionID string, typ EventType, payload map[string]any) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := l.nextSeq
	l.nextSeq++
	l.events = append(l.events, Event{
		ID:        id,
		Seq:       seq,
		Type:      typ,
		SessionID: sessionID,
		Payload:   payload,
		Timestamp: time.Now(),
	})
	return seq
}

// Events returns a copy of all events up to the given sequence (inclusive).
// If seq is 0, returns all events.
func (l *EventLog) Events(upToSeq int) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if upToSeq <= 0 || upToSeq >= len(l.events) {
		out := make([]Event, len(l.events))
		copy(out, l.events)
		return out
	}
	out := make([]Event, upToSeq)
	copy(out, l.events[:upToSeq])
	return out
}

// Len returns the number of events.
func (l *EventLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.events)
}

// Last returns the last event, or nil if empty.
func (l *EventLog) Last() *Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.events) == 0 {
		return nil
	}
	e := l.events[len(l.events)-1]
	return &e
}

// Fork creates a new event log containing events up to the given sequence.
// The forked log gets a new session ID. Events after seq are NOT included.
func (l *EventLog) Fork(seq int, newSessionID string) *EventLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	forked := NewEventLog()
	limit := seq
	if limit > len(l.events) {
		limit = len(l.events)
	}
	for i := 0; i < limit; i++ {
		e := l.events[i]
		e.SessionID = newSessionID
		e.Seq = forked.nextSeq
		forked.events = append(forked.events, e)
		forked.nextSeq++
	}
	return forked
}

// ReplayState reconstructs the session state by replaying events.
type ReplayState struct {
	SessionID     string
	Messages      []ReplayMessage
	ToolCalls     []ReplayToolCall
	PermissionAsk bool
	TokenEstimate int
	MessageCount  int
}

// ReplayMessage is a reconstructed message from events.
type ReplayMessage struct {
	Role    string
	Content string
	Step    int
}

// ReplayToolCall is a reconstructed tool call from events.
type ReplayToolCall struct {
	Name   string
	Args   map[string]any
	Result string
	Step   int
}

// Replay replays the event log and returns the reconstructed state.
func (l *EventLog) Replay() *ReplayState {
	l.mu.RLock()
	defer l.mu.RUnlock()

	state := &ReplayState{}
	for _, e := range l.events {
		state.SessionID = e.SessionID
		switch e.Type {
		case EventUserMessage:
			content, _ := e.Payload["content"].(string)
			step, _ := e.Payload["step"].(int)
			state.Messages = append(state.Messages, ReplayMessage{Role: "user", Content: content, Step: step})
			state.MessageCount++
		case EventAssistantMessage:
			content, _ := e.Payload["content"].(string)
			step, _ := e.Payload["step"].(int)
			state.Messages = append(state.Messages, ReplayMessage{Role: "assistant", Content: content, Step: step})
			state.MessageCount++
		case EventToolCall:
			name, _ := e.Payload["name"].(string)
			args, _ := e.Payload["args"].(map[string]any)
			step, _ := e.Payload["step"].(int)
			state.ToolCalls = append(state.ToolCalls, ReplayToolCall{Name: name, Args: args, Step: step})
		case EventToolResult:
			name, _ := e.Payload["name"].(string)
			result, _ := e.Payload["result"].(string)
			step, _ := e.Payload["step"].(int)
			if len(state.ToolCalls) > 0 {
				last := &state.ToolCalls[len(state.ToolCalls)-1]
				if last.Name == name && last.Result == "" {
					last.Result = result
					last.Step = step
				}
			}
		case EventPermissionAsk:
			state.PermissionAsk = true
		case EventPermissionGrant, EventPermissionDeny:
			state.PermissionAsk = false
		}
	}
	return state
}

// EventsSince returns events after the given sequence number.
func (l *EventLog) EventsSince(seq int) []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if seq >= len(l.events) {
		return nil
	}
	out := make([]Event, len(l.events)-seq)
	copy(out, l.events[seq:])
	return out
}

// String returns a human-readable summary of the event log.
func (l *EventLog) String() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fmt.Sprintf("EventLog{events=%d, nextSeq=%d}", len(l.events), l.nextSeq)
}
