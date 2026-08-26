package sse

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

func TestNewSSEHandler(t *testing.T) {
	h := NewSSEHandler()
	if h == nil {
		t.Fatal("NewSSEHandler returned nil")
	}
	if h.pool == nil {
		t.Error("expected pool to be initialized")
	}
}

func TestSSEHandler_SetPoolSize(t *testing.T) {
	h := NewSSEHandler()
	h.SetPoolSize(20)

	stats := h.Pool().Stats()
	if stats.Max != 20 {
		t.Errorf("expected max 20, got %d", stats.Max)
	}
}

func TestSSEHandler_NewConnection(t *testing.T) {
	var w http.ResponseWriter = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/test", nil)

	h := NewSSEHandler()
	conn, err := h.NewConnection(w, r, "sess-1")
	if err != nil {
		t.Fatalf("NewConnection failed: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil connection")
	}
	if conn.SessionID() != "sess-1" {
		t.Errorf("expected session sess-1, got %s", conn.SessionID())
	}
}

func TestSSEConnection_WriteEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	h := NewSSEHandler()
	connID := "conn-test"

	conn := &SSEConnection{
		id:        connID,
		sessionID: "sess-1",
		writer:    NewSSEStreamWriter(w, flusher),
		handler:   h,
		lastEvent: time.Now(),
	}

	h.Pool().Register(connID, "sess-1")
	conn.metrics = NewConnectionMetrics(connID, "sess-1")

	ev := NewStructuredEvent(EventTextDelta, "sess-1", 1)
	ev.Delta = "Hello"

	err := conn.WriteEvent(ev)
	if err != nil {
		t.Fatalf("WriteEvent failed: %v", err)
	}

	if err := conn.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	body := recorder.Body.String()
	if len(body) == 0 {
		t.Error("expected non-empty body after flush")
	}

	if conn.EventCount() != 1 {
		t.Errorf("expected 1 event, got %d", conn.EventCount())
	}
}

func TestSSEConnection_Close(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	h := NewSSEHandler()
	connID := "conn-close"

	conn := &SSEConnection{
		id:        connID,
		sessionID: "sess-1",
		writer:    NewSSEStreamWriter(w, flusher),
		handler:   h,
		lastEvent: time.Now(),
	}

	h.Pool().Register(connID, "sess-1")

	if h.Pool().ActiveCount() != 1 {
		t.Error("expected 1 active connection before close")
	}

	conn.Close()

	if h.Pool().ActiveCount() != 0 {
		t.Error("expected 0 active connections after close")
	}
}

func TestSSEConnection_Buffer(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	h := NewSSEHandler()

	conn := &SSEConnection{
		id:        "conn-buf",
		sessionID: "sess-1",
		writer:    NewSSEStreamWriter(w, flusher),
		buffer:    NewBackpressureBuffer(10),
		handler:   h,
		lastEvent: time.Now(),
	}

	if conn.Buffer() == nil {
		t.Error("expected non-nil buffer")
	}
	if conn.Buffer().Cap() != 10 {
		t.Errorf("expected buffer cap 10, got %d", conn.Buffer().Cap())
	}
}

func TestSSEConnection_Budget(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	h := NewSSEHandler()

	conn := &SSEConnection{
		id:        "conn-budget",
		sessionID: "sess-1",
		writer:    NewSSEStreamWriter(w, flusher),
		handler:   h,
		lastEvent: time.Now(),
	}

	if conn.Budget() == nil {
		t.Error("expected non-nil budget")
	}
}

func TestNewStreamEventFromEngine(t *testing.T) {
	ev := NewStreamEventFromEngine("sess-1", 1, nil)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
}

func TestParseLastEventID(t *testing.T) {
	id, err := ParseLastEventID("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if id != 0 {
		t.Errorf("expected 0 for empty header, got %d", id)
	}

	id, err = ParseLastEventID("42")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("expected 42, got %d", id)
	}

	_, err = ParseLastEventID("invalid")
	if err == nil {
		t.Error("expected error for invalid header")
	}
}

func TestDefaultStreamOptions(t *testing.T) {
	opts := DefaultStreamOptions()
	if !opts.AutoReconnect {
		t.Error("expected AutoReconnect to be true by default")
	}
}

func TestSSEConnection_IDAndSessionID(t *testing.T) {
	conn := &SSEConnection{
		id:        "conn-123",
		sessionID: "sess-456",
	}

	if conn.ID() != "conn-123" {
		t.Errorf("expected ID conn-123, got %s", conn.ID())
	}
	if conn.SessionID() != "sess-456" {
		t.Errorf("expected SessionID sess-456, got %s", conn.SessionID())
	}
}

func TestSSEConnection_LastEventTime(t *testing.T) {
	conn := &SSEConnection{
		lastEvent: time.Now(),
	}

	time.Sleep(50 * time.Millisecond)
	updated := conn.LastEventTime()

	if !updated.After(conn.lastEvent) && !updated.Equal(conn.lastEvent) {
		t.Error("LastEventTime should return the stored time")
	}
}

func TestSSEConnection_WriteComment(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)

	conn := &SSEConnection{
		id:        "conn-cmt",
		sessionID: "sess-1",
		writer:    NewSSEStreamWriter(w, flusher),
		lastEvent: time.Now(),
	}

	err := conn.WriteComment("test comment")
	if err != nil {
		t.Fatalf("WriteComment failed: %v", err)
	}
}

func TestSSEStreamRunner_Integration(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	r := httptest.NewRequest("POST", "/test", nil)

	h := NewSSEHandler()
	runner, err := h.NewStreamRunner(w, r, "sess-integration")
	if err != nil {
		t.Fatalf("NewStreamRunner failed: %v", err)
	}

	runner.Start()

	engineEvent := &engine.Event{
		Type:    engine.EventTextDelta,
		Content: "Integration test",
	}
	runner.SendEvent(nil)
	runner.SendEvent(engineEvent)

	time.Sleep(200 * time.Millisecond)
	runner.Close()

	body := recorder.Body.String()
	if len(body) == 0 {
		t.Error("expected non-empty body from integration test")
	}
}
