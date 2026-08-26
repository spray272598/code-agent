package sse

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStructuredEvent_Marshal(t *testing.T) {
	ev := NewStructuredEvent(EventTextDelta, "session-123", 42)
	ev.Delta = "Hello World"
	ev.Step = 3

	data, err := ev.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded["type"] != string(EventTextDelta) {
		t.Errorf("expected type %s, got %v", EventTextDelta, decoded["type"])
	}
	if decoded["seq"].(float64) != 42 {
		t.Errorf("expected seq 42, got %v", decoded["seq"])
	}
	if decoded["sessionId"] != "session-123" {
		t.Errorf("expected sessionId session-123, got %v", decoded["sessionId"])
	}
	if decoded["delta"] != "Hello World" {
		t.Errorf("expected delta Hello World, got %v", decoded["delta"])
	}
	if decoded["step"].(float64) != 3 {
		t.Errorf("expected step 3, got %v", decoded["step"])
	}
}

func TestStructuredEvent_SSEEventID(t *testing.T) {
	ev := NewStructuredEvent(EventDone, "session-1", 100)
	if ev.SSEEventID() != "100" {
		t.Errorf("expected SSEEventID 100, got %s", ev.SSEEventID())
	}

	ev2 := NewStructuredEvent(EventDone, "session-1", 0)
	if ev2.SSEEventID() != "" {
		t.Errorf("expected empty SSEEventID, got %s", ev2.SSEEventID())
	}
}

func TestNewHeartbeatEvent(t *testing.T) {
	ev := NewHeartbeatEvent("session-hb", 1)
	if ev.Type != EventHeartbeat {
		t.Errorf("expected heartbeat type, got %s", ev.Type)
	}
	if ev.SessionID != "session-hb" {
		t.Errorf("expected session-hb, got %s", ev.SessionID)
	}
}

func TestNewErrorEvent(t *testing.T) {
	ev := NewErrorEvent("session-err", 5, "something went wrong")
	if ev.Type != EventError {
		t.Errorf("expected error type, got %s", ev.Type)
	}
	if !ev.Completed {
		t.Error("expected Completed to be true for error event")
	}
	if ev.Content != "something went wrong" {
		t.Errorf("expected error message, got %s", ev.Content)
	}
}

func TestNewDoneEvent(t *testing.T) {
	ev := NewDoneEvent("session-done", 10)
	if ev.Type != EventDone {
		t.Errorf("expected done type, got %s", ev.Type)
	}
	if !ev.Completed {
		t.Error("expected Completed to be true for done event")
	}
}

func TestStructuredEvent_ReasoningMeta(t *testing.T) {
	ev := NewStructuredEvent(EventReasoningDelta, "session-r", 1)
	ev.Reasoning = &ReasoningMeta{
		Delta:   "let me think...",
		TokenID: 42,
		Phase:   "analysis",
	}

	data, err := ev.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	reasoning, ok := decoded["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatal("expected reasoning to be present")
	}
	if reasoning["delta"] != "let me think..." {
		t.Errorf("expected reasoning delta, got %v", reasoning["delta"])
	}
	if reasoning["tokenId"].(float64) != 42 {
		t.Errorf("expected tokenId 42, got %v", reasoning["tokenId"])
	}
}

func TestStructuredEvent_TokenUsage(t *testing.T) {
	ev := NewStructuredEvent(EventAnswer, "session-t", 1)
	ev.Usage = &TokenUsage{
		InputTokens:  1000,
		OutputTokens: 500,
		TotalTokens:  1500,
	}

	data, err := ev.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	usage, ok := decoded["usage"].(map[string]interface{})
	if !ok {
		t.Fatal("expected usage to be present")
	}
	if usage["inputTokens"].(float64) != 1000 {
		t.Errorf("expected inputTokens 1000, got %v", usage["inputTokens"])
	}
	if usage["totalTokens"].(float64) != 1500 {
		t.Errorf("expected totalTokens 1500, got %v", usage["totalTokens"])
	}
}

func TestStructuredEvent_Timestamp(t *testing.T) {
	before := time.Now().UnixMilli()
	ev := NewStructuredEvent(EventSystem, "session-ts", 1)
	after := time.Now().UnixMilli()

	if ev.Timestamp < before || ev.Timestamp > after {
		t.Errorf("timestamp %d not in range [%d, %d]", ev.Timestamp, before, after)
	}
}
