package sse

import (
	"testing"
	"time"
)

func TestNewConnectionMetrics(t *testing.T) {
	m := NewConnectionMetrics("conn-1", "sess-1")
	if m == nil {
		t.Fatal("NewConnectionMetrics returned nil")
	}
	if m.ID != "conn-1" {
		t.Errorf("expected ID conn-1, got %s", m.ID)
	}
	if m.SessionID != "sess-1" {
		t.Errorf("expected SessionID sess-1, got %s", m.SessionID)
	}
}

func TestConnectionMetrics_RecordEvent(t *testing.T) {
	m := NewConnectionMetrics("conn-1", "sess-1")

	m.RecordEvent(EventTextDelta, 100)
	m.RecordEvent(EventTextDelta, 150)
	m.RecordEvent(EventDone, 50)

	if m.EventCount.Load() != 3 {
		t.Errorf("expected 3 events, got %d", m.EventCount.Load())
	}
	if m.BytesSent.Load() != 300 {
		t.Errorf("expected 300 bytes, got %d", m.BytesSent.Load())
	}
	if m.LastEventType != string(EventDone) {
		t.Errorf("expected last event type done, got %s", m.LastEventType)
	}
}

func TestConnectionMetrics_RecordDrop(t *testing.T) {
	m := NewConnectionMetrics("conn-1", "sess-1")
	m.RecordDrop()
	m.RecordDrop()

	if m.EventsDropped.Load() != 2 {
		t.Errorf("expected 2 drops, got %d", m.EventsDropped.Load())
	}
}

func TestConnectionMetrics_RecordReconnect(t *testing.T) {
	m := NewConnectionMetrics("conn-1", "sess-1")
	m.RecordReconnect()

	if m.Reconnects.Load() != 1 {
		t.Errorf("expected 1 reconnect, got %d", m.Reconnects.Load())
	}
}

func TestConnectionMetrics_Duration(t *testing.T) {
	m := NewConnectionMetrics("conn-1", "sess-1")
	time.Sleep(50 * time.Millisecond)

	duration := m.Duration()
	if duration < 50*time.Millisecond {
		t.Errorf("expected duration >= 50ms, got %v", duration)
	}
}

func TestConnectionMetrics_EventsByTypeSnapshot(t *testing.T) {
	m := NewConnectionMetrics("conn-1", "sess-1")

	m.RecordEvent(EventTextDelta, 100)
	m.RecordEvent(EventTextDelta, 100)
	m.RecordEvent(EventDone, 50)

	snapshot := m.EventsByTypeSnapshot()
	if snapshot["text_delta"] != 2 {
		t.Errorf("expected 2 text_delta events, got %d", snapshot["text_delta"])
	}
	if snapshot["done"] != 1 {
		t.Errorf("expected 1 done event, got %d", snapshot["done"])
	}
}

func TestConnectionPool_Register(t *testing.T) {
	pool := NewConnectionPool(10)

	m, err := pool.Register("conn-1", "sess-1")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}

	if pool.ActiveCount() != 1 {
		t.Errorf("expected 1 active connection, got %d", pool.ActiveCount())
	}
	if pool.TotalCreated() != 1 {
		t.Errorf("expected 1 total created, got %d", pool.TotalCreated())
	}
}

func TestConnectionPool_Unregister(t *testing.T) {
	pool := NewConnectionPool(10)

	pool.Register("conn-1", "sess-1")
	pool.Unregister("conn-1")

	if pool.ActiveCount() != 0 {
		t.Errorf("expected 0 active connections, got %d", pool.ActiveCount())
	}
	if pool.TotalClosed() != 1 {
		t.Errorf("expected 1 total closed, got %d", pool.TotalClosed())
	}
}

func TestConnectionPool_Full(t *testing.T) {
	pool := NewConnectionPool(2)

	pool.Register("conn-1", "sess-1")
	pool.Register("conn-2", "sess-2")

	_, err := pool.Register("conn-3", "sess-3")
	if err != ErrPoolFull {
		t.Errorf("expected ErrPoolFull, got %v", err)
	}
}

func TestConnectionPool_Stats(t *testing.T) {
	pool := NewConnectionPool(10)

	pool.Register("conn-1", "sess-1")
	pool.Register("conn-2", "sess-2")
	pool.RecordEvent(EventTextDelta, 100)
	pool.RecordDrop()

	stats := pool.Stats()
	if stats.Active != 2 {
		t.Errorf("expected 2 active, got %d", stats.Active)
	}
	if stats.Max != 10 {
		t.Errorf("expected max 10, got %d", stats.Max)
	}
	if stats.UsageRate < 0.15 || stats.UsageRate > 0.25 {
		t.Errorf("expected usage rate around 0.2, got %f", stats.UsageRate)
	}
	if stats.TotalEvents != 1 {
		t.Errorf("expected 1 total events, got %d", stats.TotalEvents)
	}
	if stats.TotalDropped != 1 {
		t.Errorf("expected 1 total dropped, got %d", stats.TotalDropped)
	}
}

func TestConnectionPool_DefaultMax(t *testing.T) {
	pool := NewConnectionPool(0)
	if pool.maxConnections != 100 {
		t.Errorf("expected default max 100, got %d", pool.maxConnections)
	}
}

func TestConnectionPool_ListActive(t *testing.T) {
	pool := NewConnectionPool(5)

	pool.Register("conn-1", "sess-1")
	pool.Register("conn-2", "sess-2")

	active := pool.ListActive()
	if len(active) != 2 {
		t.Errorf("expected 2 active, got %d", len(active))
	}
}
