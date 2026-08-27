package model

import (
	"sync"
	"testing"
)

func TestEventLogAppend(t *testing.T) {
	l := NewEventLog()
	seq := l.Append("e1", "s1", EventUserMessage, map[string]any{"content": "hello"})
	if seq != 1 {
		t.Errorf("expected seq 1, got %d", seq)
	}
	if l.Len() != 1 {
		t.Errorf("expected 1 event, got %d", l.Len())
	}
}

func TestEventLogSequence(t *testing.T) {
	l := NewEventLog()
	s1 := l.Append("e1", "s1", EventUserMessage, nil)
	s2 := l.Append("e2", "s1", EventAssistantMessage, nil)
	s3 := l.Append("e3", "s1", EventToolCall, nil)
	if s1 != 1 || s2 != 2 || s3 != 3 {
		t.Errorf("sequence mismatch: %d, %d, %d", s1, s2, s3)
	}
}

func TestEventLogEvents(t *testing.T) {
	l := NewEventLog()
	l.Append("e1", "s1", EventUserMessage, nil)
	l.Append("e2", "s1", EventAssistantMessage, nil)
	l.Append("e3", "s1", EventToolCall, nil)

	events := l.Events(0)
	if len(events) != 3 {
		t.Fatalf("expected 3, got %d", len(events))
	}

	events = l.Events(2)
	if len(events) != 2 {
		t.Fatalf("expected 2, got %d", len(events))
	}
	if events[1].Type != EventAssistantMessage {
		t.Errorf("expected assistant message, got %s", events[1].Type)
	}
}

func TestEventLogLast(t *testing.T) {
	l := NewEventLog()
	if l.Last() != nil {
		t.Error("empty log should return nil")
	}
	l.Append("e1", "s1", EventUserMessage, nil)
	l.Append("e2", "s1", EventToolCall, nil)
	last := l.Last()
	if last == nil || last.Type != EventToolCall {
		t.Errorf("expected tool call, got %v", last)
	}
}

func TestEventLogFork(t *testing.T) {
	l := NewEventLog()
	l.Append("e1", "s1", EventUserMessage, map[string]any{"content": "hello"})
	l.Append("e2", "s1", EventAssistantMessage, map[string]any{"content": "response"})
	l.Append("e3", "s1", EventToolCall, map[string]any{"name": "bash"})

	forked := l.Fork(2, "s2")
	if forked.Len() != 2 {
		t.Fatalf("forked should have 2 events, got %d", forked.Len())
	}
	if l.Len() != 3 {
		t.Error("original should still have 3 events")
	}

	// Forked events should have new session ID
	for _, e := range forked.Events(0) {
		if e.SessionID != "s2" {
			t.Errorf("forked event should have session s2, got %s", e.SessionID)
		}
	}

	// Forked events should have sequential seq
	events := forked.Events(0)
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("forked seq should restart: %d, %d", events[0].Seq, events[1].Seq)
	}
}

func TestEventLogForkPreservesOriginal(t *testing.T) {
	l := NewEventLog()
	l.Append("e1", "s1", EventUserMessage, nil)
	l.Append("e2", "s1", EventAssistantMessage, nil)

	_ = l.Fork(1, "s2")
	if l.Len() != 2 {
		t.Error("fork should not modify original")
	}
}

func TestReplay(t *testing.T) {
	l := NewEventLog()
	l.Append("e1", "s1", EventUserMessage, map[string]any{"content": "hello", "step": 0})
	l.Append("e2", "s1", EventAssistantMessage, map[string]any{"content": "thinking", "step": 0})
	l.Append("e3", "s1", EventToolCall, map[string]any{"name": "bash", "args": map[string]any{"command": "ls"}, "step": 1})
	l.Append("e4", "s1", EventToolResult, map[string]any{"name": "bash", "result": "file.go", "step": 1})
	l.Append("e5", "s1", EventAssistantMessage, map[string]any{"content": "Final Answer: done", "step": 2})

	state := l.Replay()
	if state.MessageCount != 3 {
		t.Errorf("expected 3 messages, got %d", state.MessageCount)
	}
	if len(state.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(state.Messages))
	}
	if state.Messages[0].Role != "user" || state.Messages[0].Content != "hello" {
		t.Errorf("first message wrong: %+v", state.Messages[0])
	}
	if len(state.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(state.ToolCalls))
	}
	if state.ToolCalls[0].Name != "bash" || state.ToolCalls[0].Result != "file.go" {
		t.Errorf("tool call wrong: %+v", state.ToolCalls[0])
	}
}

func TestEventsSince(t *testing.T) {
	l := NewEventLog()
	l.Append("e1", "s1", EventUserMessage, nil)
	l.Append("e2", "s1", EventAssistantMessage, nil)
	l.Append("e3", "s1", EventToolCall, nil)

	events := l.EventsSince(1)
	if len(events) != 2 {
		t.Fatalf("expected 2, got %d", len(events))
	}
	if events[0].Seq != 2 {
		t.Errorf("expected seq 2, got %d", events[0].Seq)
	}
}

func TestEventLogConcurrent(t *testing.T) {
	l := NewEventLog()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.Append("", "s1", EventUserMessage, nil)
		}(i)
	}
	wg.Wait()
	if l.Len() != 100 {
		t.Errorf("expected 100, got %d", l.Len())
	}
}
