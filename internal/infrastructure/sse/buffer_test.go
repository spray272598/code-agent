package sse

import (
	"testing"
)

func TestGetEventPriority(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  EventPriority
	}{
		{EventDone, PriorityCritical},
		{EventError, PriorityCritical},
		{EventCancel, PriorityCritical},
		{EventSystem, PriorityCritical},
		{EventPermission, PriorityCritical},
		{EventCheckpoint, PriorityCritical},
		{EventReasoningDelta, PriorityLow},
		{EventTextDelta, PriorityLow},
		{EventToolCallDelta, PriorityNormal},
		{EventToolResult, PriorityNormal},
		{EventAnswer, PriorityNormal},
		{EventHeartbeat, PriorityNormal},
	}

	for _, tt := range tests {
		t.Run(string(tt.eventType), func(t *testing.T) {
			got := GetEventPriority(tt.eventType)
			if got != tt.expected {
				t.Errorf("GetEventPriority(%s) = %d, want %d", tt.eventType, got, tt.expected)
			}
		})
	}
}

func TestBackpressureBuffer_BasicSendReceive(t *testing.T) {
	buf := NewBackpressureBuffer(16)
	defer buf.Close()

	ev := NewStructuredEvent(EventTextDelta, "session-1", 1)
	ev.Delta = "test"

	if !buf.Send(ev) {
		t.Fatal("Send should succeed for empty buffer")
	}

	recv, ok := buf.Receive()
	if !ok {
		t.Fatal("Receive should succeed")
	}
	if recv.Seq != 1 {
		t.Errorf("expected seq 1, got %d", recv.Seq)
	}
}

func TestBackpressureBuffer_DropLowPriority(t *testing.T) {
	buf := NewBackpressureBuffer(5)
	defer buf.Close()

	for i := 0; i < 4; i++ {
		ev := NewStructuredEvent(EventReasoningDelta, "session-1", uint64(i))
		ev.Delta = "low-priority"
		if !buf.Send(ev) {
			t.Fatalf("Send %d should succeed", i)
		}
	}

	if !buf.IsHighWatermark() {
		t.Error("should be at high watermark after filling buffer")
	}

	lowEv := NewStructuredEvent(EventReasoningDelta, "session-1", 100)
	lowEv.Delta = "should be dropped"
	if buf.Send(lowEv) {
		t.Error("low priority event should be dropped at high watermark")
	}

	if buf.DropCount() == 0 {
		t.Error("drop count should be > 0")
	}
}

func TestBackpressureBuffer_AcceptCriticalAtHighWatermark(t *testing.T) {
	buf := NewBackpressureBuffer(5)
	defer buf.Close()

	for i := 0; i < 5; i++ {
		ev := NewStructuredEvent(EventReasoningDelta, "session-1", uint64(i))
		buf.Send(ev)
	}

	criticalEv := NewStructuredEvent(EventDone, "session-1", 99)
	if !buf.Send(criticalEv) {
		t.Error("critical event should be accepted even at high watermark")
	}
}

func TestBackpressureBuffer_Usage(t *testing.T) {
	buf := NewBackpressureBuffer(10)
	defer buf.Close()

	if buf.Usage() != 0 {
		t.Error("initial usage should be 0")
	}

	for i := 0; i < 5; i++ {
		ev := NewStructuredEvent(EventTextDelta, "session-1", uint64(i))
		buf.Send(ev)
	}

	usage := buf.Usage()
	if usage < 0.4 || usage > 0.6 {
		t.Errorf("expected usage around 0.5, got %f", usage)
	}
}

func TestBackpressureBuffer_Stats(t *testing.T) {
	buf := NewBackpressureBuffer(8)
	defer buf.Close()

	for i := 0; i < 4; i++ {
		ev := NewStructuredEvent(EventTextDelta, "session-1", uint64(i))
		buf.Send(ev)
	}

	stats := buf.Stats()
	if stats.Len != 4 {
		t.Errorf("expected len 4, got %d", stats.Len)
	}
	if stats.Cap != 8 {
		t.Errorf("expected cap 8, got %d", stats.Cap)
	}
	if stats.Usage < 0.4 || stats.Usage > 0.6 {
		t.Errorf("expected usage around 0.5, got %f", stats.Usage)
	}
}

func TestBackpressureBuffer_NilEvent(t *testing.T) {
	buf := NewBackpressureBuffer(4)
	defer buf.Close()

	if buf.Send(nil) {
		t.Error("sending nil event should fail")
	}
}

func TestBackpressureBuffer_DefaultSize(t *testing.T) {
	buf := NewBackpressureBuffer(0)
	if buf.Cap() != DefaultBufferSize {
		t.Errorf("expected default buffer size %d, got %d", DefaultBufferSize, buf.Cap())
	}
}
