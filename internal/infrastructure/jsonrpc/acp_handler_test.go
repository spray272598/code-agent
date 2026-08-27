package jsonrpc

import (
	"context"
	"encoding/json"
	"testing"
)

type mockACPApp struct {
	promptCalled  bool
	cancelCalled  bool
	controlCalled bool
	lastSessionID string
	lastPrompt    string
	lastSignal    int
}

func (m *mockACPApp) RunBackground(ctx context.Context, req ACPChatRequest, onEvent func(map[string]any)) (string, error) {
	m.promptCalled = true
	m.lastSessionID = req.SessionID
	m.lastPrompt = req.Message
	return req.SessionID, nil
}

func (m *mockACPApp) CancelSession(sessionID, reason string) (bool, error) {
	m.cancelCalled = true
	m.lastSessionID = sessionID
	return true, nil
}

func (m *mockACPApp) SendControl(sessionID string, signal int, goal string) bool {
	m.controlCalled = true
	m.lastSessionID = sessionID
	m.lastSignal = signal
	return true
}

func (m *mockACPApp) UsageSnapshot(ctx context.Context, userID, sessionID string) any {
	return map[string]any{"session_used": 1}
}

func acpCall(t *testing.T, handler *ACPHandler, method string, params any) json.RawMessage {
	t.Helper()
	server := NewServer()
	handler.RegisterHandlers(server)
	id := MustID("test-1")
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		paramsRaw = b
	}
	result, err := server.CallHandler(context.Background(), id, method, paramsRaw)
	if err != nil {
		t.Fatalf("call %s failed: %v", method, err)
	}
	b, _ := json.Marshal(result)
	return b
}

func TestACPInitialize(t *testing.T) {
	app := &mockACPApp{}
	handler := NewACPHandler(app)

	result := acpCall(t, handler, "initialize", nil)

	var r acpInitializeResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if r.ProtocolVersion != "1.0.0" {
		t.Errorf("expected 1.0.0, got %s", r.ProtocolVersion)
	}
	if _, ok := r.Capabilities["sessions"]; !ok {
		t.Error("expected sessions capability")
	}
}

func TestACPSessionNew(t *testing.T) {
	app := &mockACPApp{}
	handler := NewACPHandler(app)

	result := acpCall(t, handler, "session/new", map[string]any{
		"userId": "test-user",
	})

	var r map[string]string
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if r["sessionId"] == "" {
		t.Error("expected sessionId")
	}
}

func TestACPSessionPrompt(t *testing.T) {
	app := &mockACPApp{}
	handler := NewACPHandler(app)

	result := acpCall(t, handler, "session/prompt", map[string]any{
		"sessionId": "acp-123",
		"prompt":    "hello",
		"userId":    "test",
	})

	var r map[string]any
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if !app.promptCalled {
		t.Error("expected RunBackground to be called")
	}
	if r["accepted"] != true {
		t.Error("expected accepted=true")
	}
}

func TestACPSessionCancel(t *testing.T) {
	app := &mockACPApp{}
	handler := NewACPHandler(app)

	result := acpCall(t, handler, "session/cancel", map[string]any{
		"sessionId": "acp-123",
	})

	var r map[string]any
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if !app.cancelCalled {
		t.Error("expected CancelSession to be called")
	}
	if r["cancelled"] != true {
		t.Error("expected cancelled=true")
	}
}

func TestACPSessionControl(t *testing.T) {
	app := &mockACPApp{}
	handler := NewACPHandler(app)

	result := acpCall(t, handler, "session/control", map[string]any{
		"sessionId": "acp-123",
		"signal":    "pause",
	})

	var r map[string]any
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if !app.controlCalled {
		t.Error("expected SendControl to be called")
	}
	if app.lastSignal != 3 {
		t.Errorf("expected signal 3 (pause), got %d", app.lastSignal)
	}
}

func TestACPSessionStatus(t *testing.T) {
	app := &mockACPApp{}
	handler := NewACPHandler(app)

	result := acpCall(t, handler, "session/status", map[string]any{
		"sessionId": "acp-123",
	})

	var r map[string]any
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if r["sessionId"] != "acp-123" {
		t.Errorf("expected acp-123, got %s", r["sessionId"])
	}
}
