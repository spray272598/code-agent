package sse

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

func TestDoomLoopDetector_TailRepeat(t *testing.T) {
	d := NewDoomLoopDetector()
	if d.IsDoomed() {
		t.Error("should not be doomed initially")
	}

	for i := 0; i < DefaultDoomTailRepeatThreshold+1; i++ {
		d.RecordDelta("loop", true)
	}

	if !d.IsDoomed() {
		t.Error("should be doomed after tail repeat threshold")
	}

	stats := d.Stats()
	if reason, ok := stats["abortReason"]; !ok || reason == "" {
		t.Error("should have abort reason")
	}
}

func TestDoomLoopDetector_ReasoningBudget(t *testing.T) {
	d := NewDoomLoopDetector()
	for i := 0; i < 1000; i++ {
		d.RecordDelta(strings.Repeat("a", 50), true)
	}
	if !d.IsDoomed() {
		t.Error("should be doomed after reasoning budget")
	}
}

func TestDoomLoopDetector_StreamByteLimit(t *testing.T) {
	d := NewDoomLoopDetector()
	for i := 0; i < 1000; i++ {
		d.RecordDelta(strings.Repeat("x", 8000), false)
	}
	if !d.IsDoomed() {
		t.Error("should be doomed after stream byte limit")
	}
}

func TestDoomLoopDetector_Reset(t *testing.T) {
	d := NewDoomLoopDetector()
	for i := 0; i < DefaultDoomTailRepeatThreshold; i++ {
		d.RecordDelta("a", true)
	}
	d.Reset()
	if d.IsDoomed() {
		t.Error("should not be doomed after reset")
	}
}

func TestDoomLoopDetector_StartNewAttempt(t *testing.T) {
	d := NewDoomLoopDetector()
	d.StartNewAttempt()
	stats := d.Stats()
	if ac, ok := stats["attemptCount"].(int); !ok || ac < 1 {
		t.Error("attempt count should be >= 1")
	}
}

func TestStreamingTurnCapture_AppendAndFinalize(t *testing.T) {
	cap := NewStreamingTurnCapture(8 * 1024 * 1024)
	cap.BeginTurn("p1", 1)
	cap.StartGeneration(1000)
	cap.PushReasoningDelta("thinking...")
	cap.PushTextDelta("answer text")
	cap.MarkToolCall()
	cap.FinalizeForUpload()

	if cap.ReasoningText != "thinking..." {
		t.Errorf("unexpected reasoning text: %q", cap.ReasoningText)
	}
	if cap.ResponseText != "answer text" {
		t.Errorf("unexpected response text: %q", cap.ResponseText)
	}
	if cap.Phase != CaptureToolCall {
		t.Errorf("expected ToolCall phase, got %v", cap.Phase)
	}
}

func TestStreamingTurnCapture_MultiAttempt(t *testing.T) {
	cap := NewStreamingTurnCapture(8 * 1024 * 1024)
	cap.BeginTurn("p1", 1)

	cap.StartGeneration(1000)
	cap.PushReasoningDelta("attempt1 reasoning")

	cap.StartGeneration(2000)
	cap.PushReasoningDelta("attempt2 reasoning")
	cap.PushTextDelta("final answer")

	cap.FinalizeForUpload()

	if len(cap.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(cap.Segments))
	}
	if cap.AttemptCount != 2 {
		t.Errorf("expected attempt count 2, got %d", cap.AttemptCount)
	}
}

func TestStreamingTurnCapture_Empty(t *testing.T) {
	cap := NewStreamingTurnCapture(8 * 1024 * 1024)
	cap.BeginTurn("p1", 1)
	if !cap.IsEmpty() {
		t.Error("empty capture should report empty")
	}
}

func TestStreamingTurnCapture_Truncation(t *testing.T) {
	cap := NewStreamingTurnCapture(100)
	cap.BeginTurn("p1", 1)
	cap.StartGeneration(1000)
	cap.PushReasoningDelta(strings.Repeat("x", 500))
	if !cap.Truncated {
		t.Error("should be truncated")
	}
}

func TestReconnectManager_Backoff(t *testing.T) {
	rm := NewReconnectManager(DefaultReconnectPolicy())

	if !rm.CanRetry() {
		t.Error("should be able to retry initially")
	}

	rm.NextBackoff()
	rm.NextBackoff()
	rm.NextBackoff()
	rm.NextBackoff()
	rm.NextBackoff()

	if rm.CanRetry() {
		t.Error("should not be able to retry after max attempts")
	}
}

func TestReconnectManager_LastEventID(t *testing.T) {
	rm := NewReconnectManager(DefaultReconnectPolicy())
	if rm.LastEventID() != 0 {
		t.Error("initial last event id should be 0")
	}

	rm.SetLastEventID(42)
	if rm.LastEventID() != 42 {
		t.Errorf("expected 42, got %d", rm.LastEventID())
	}
}

func TestReconnectManager_Success(t *testing.T) {
	rm := NewReconnectManager(DefaultReconnectPolicy())
	rm.NextBackoff()
	rm.NextBackoff()
	rm.RecordSuccess()
	count, _, _ := rm.Stats()
	if count != 0 {
		t.Errorf("retry count should reset to 0 on success, got %d", count)
	}
}

func TestSSEObserve(t *testing.T) {
	initial := SSEActiveConnections()
	SSEObserveConnection()
	if SSEActiveConnections() != initial+1 {
		t.Error("connection count should increment")
	}
	SSEObserveDisconnection()
	if SSEActiveConnections() != initial {
		t.Error("connection count should decrement")
	}

	SSEObserveEvent()
	if SSETotalEvents() < 1 {
		t.Error("event count should increment")
	}

	SSEObserveBytes(100)
	if SSETotalBytes() < 100 {
		t.Error("bytes should increment")
	}

	SSEObserveHeartbeat()
	if SSEHeartbeatsSent() < 1 {
		t.Error("heartbeats should increment")
	}

	SSEObserveDoomLoop()
	if SSEDoomLoopDetected() < 1 {
		t.Error("doom loop count should increment")
	}
}

func TestConnectionPool_Integration(t *testing.T) {
	h := NewSSEHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	runner, err := h.NewStreamRunner(rec, req, "sess-1")
	if err != nil {
		t.Fatal(err)
	}

	if got := h.Pool().ActiveCount(); got < 1 {
		t.Errorf("expected active count >=1, got %d", got)
	}
	if got := h.Pool().TotalCreated(); got < 1 {
		t.Errorf("expected total created >=1, got %d", got)
	}

	// SSEConnection.Close should unregister
	runner.conn.Close()
	if got := h.Pool().ActiveCount(); got != 0 {
		t.Errorf("expected active count 0 after close, got %d", got)
	}
	if got := h.Pool().TotalClosed(); got < 1 {
		t.Errorf("expected total closed >=1, got %d", got)
	}
}

func TestSSEStreamRunner_BackpressureDrops(t *testing.T) {
	h := NewSSEHandler()
	h.SetPoolSize(10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	runner, err := h.NewStreamRunner(rec, req, "sess-2")
	if err != nil {
		t.Fatal(err)
	}
	runner.Start()

	// Send events (some may be dropped due to buffer high watermark)
	for i := 0; i < 64; i++ {
		runner.SendEvent(&engine.Event{Type: engine.EventTextDelta, Content: "x"})
	}

	// Give the loop time to process some events
	time.Sleep(100 * time.Millisecond)

	// Buffer should have been exercised; just verify no panic
	_ = h.Pool().Stats()

	runner.Close()
}

func TestSSEStreamRunner_GracefulClose(t *testing.T) {
	h := NewSSEHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	runner, err := h.NewStreamRunner(rec, req, "sess-3")
	if err != nil {
		t.Fatal(err)
	}
	runner.Start()

	// Send some events to make the loop active
	for i := 0; i < 3; i++ {
		runner.SendEvent(&engine.Event{Type: engine.EventTextDelta, Content: "hello"})
	}

	time.Sleep(100 * time.Millisecond)

	err = runner.GracefulClose(2 * time.Second)
	if err != nil {
		t.Errorf("graceful close returned error: %v", err)
	}

	if got := h.Pool().ActiveCount(); got != 0 {
		t.Errorf("expected active count 0 after graceful close, got %d", got)
	}
}

func TestStreamingTurnCapture_WithEngineEventFlow(t *testing.T) {
	cap := NewStreamingTurnCapture(8 * 1024 * 1024)
	cap.BeginTurn("p-eng", 1)
	cap.StartGeneration(1000)

	cap.PushReasoningDelta("analyzing...")
	cap.PushReasoningDelta("considering options")
	cap.PushTextDelta("answer is:")
	cap.PushTextDelta("42")
	cap.MarkToolCall()
	cap.SetFinishReason("stop")

	cap.FinalizeForUpload()

	if cap.AttemptCount != 1 {
		t.Errorf("expected attempt count 1, got %d", cap.AttemptCount)
	}
	if !strings.Contains(cap.ReasoningText, "analyzing") {
		t.Errorf("reasoning text missing expected content: %q", cap.ReasoningText)
	}
	if !strings.Contains(cap.ResponseText, "42") {
		t.Errorf("response text missing expected content: %q", cap.ResponseText)
	}
	if cap.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", cap.FinishReason)
	}
	if cap.Phase != CaptureToolCall {
		t.Errorf("expected ToolCall phase, got %v", cap.Phase)
	}
}
