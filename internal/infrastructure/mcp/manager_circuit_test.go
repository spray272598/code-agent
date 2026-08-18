package mcp

import (
	"errors"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

var errBoom = errors.New("boom")

func TestBackoffFor(t *testing.T) {
	cases := []struct {
		fail int
		want time.Duration
	}{
		{1, 15 * time.Second},
		{2, 30 * time.Second},
		{3, 60 * time.Second},
		{4, 120 * time.Second},
		{5, 240 * time.Second},
		{6, 5 * time.Minute}, // capped at maxBackoff
		{10, 5 * time.Minute},
	}
	for _, c := range cases {
		if got := backoffFor(c.fail); got != c.want {
			t.Errorf("backoffFor(%d) = %v, want %v", c.fail, got, c.want)
		}
	}
}

func TestRecordFailure_RetryThenOpen(t *testing.T) {
	m := NewManager()
	defer m.Close()

	m.recordFailure("s", errBoom)
	st := m.states["s"]
	if st == nil || st.state != stateRetry || st.failCount != 1 {
		t.Fatalf("after 1st failure: state=%v fail=%d, want retry/1", st.state, st.failCount)
	}
	if st.nextRetryAt.IsZero() {
		t.Fatal("nextRetryAt should be set in retry state")
	}

	for i := 2; i <= 4; i++ {
		m.recordFailure("s", errBoom)
		st = m.states["s"]
		if st.state != stateRetry || st.failCount != i {
			t.Fatalf("after %d failures: state=%v fail=%d, want retry/%d", i, st.state, st.failCount, i)
		}
	}

	// 5th consecutive failure opens the circuit.
	m.recordFailure("s", errBoom)
	st = m.states["s"]
	if st.state != stateOpen || st.failCount != maxConsecutiveFailures {
		t.Fatalf("after %d failures: state=%v fail=%d, want open/%d",
			maxConsecutiveFailures, st.state, st.failCount, maxConsecutiveFailures)
	}
	if st.openUntil.IsZero() {
		t.Fatal("openUntil should be set in open state")
	}
}

func TestRecordSuccess_ResetsToNormal(t *testing.T) {
	m := NewManager()
	defer m.Close()
	for i := 0; i < maxConsecutiveFailures; i++ {
		m.recordFailure("s", errBoom)
	}
	if m.states["s"].state != stateOpen {
		t.Fatalf("want open, got %v", m.states["s"].state)
	}

	m.recordSuccess("s")
	st := m.states["s"]
	if st.state != stateNormal || st.failCount != 0 {
		t.Fatalf("after success: state=%v fail=%d, want normal/0", st.state, st.failCount)
	}
	if !st.nextRetryAt.IsZero() || !st.openUntil.IsZero() {
		t.Fatal("timers should be reset after success")
	}
}

func TestRecordFailure_HalfOpenBackToOpen(t *testing.T) {
	m := NewManager()
	defer m.Close()
	for i := 0; i < maxConsecutiveFailures; i++ {
		m.recordFailure("s", errBoom)
	}

	m.mu.Lock()
	m.states["s"].state = stateHalfOpen
	oldOpenUntil := m.states["s"].openUntil
	m.mu.Unlock()

	time.Sleep(time.Millisecond) // ensure the refreshed window advances
	m.recordFailure("s", errBoom)

	st := m.states["s"]
	if st.state != stateOpen {
		t.Fatalf("half-open failure: state=%v, want open", st.state)
	}
	if !st.openUntil.After(oldOpenUntil) {
		t.Fatal("openUntil should be refreshed after half-open failure")
	}
}

func TestReconnectOffline_SkipsWhenBackingOffOrOpen(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// Enabled config with an empty command: reconnect would fail fast without
	// spawning a subprocess. We only assert the skip path, so no reconnect runs.
	m.mu.Lock()
	m.configs["dead"] = model.ServerConfig{Name: "dead", Transport: "stdio", Enabled: true}
	m.states["dead"] = &connState{state: stateOpen, openUntil: time.Now().Add(time.Hour)}
	m.mu.Unlock()

	m.reconnectOffline()

	m.mu.RLock()
	st := m.states["dead"]
	m.mu.RUnlock()
	if st.state != stateOpen {
		t.Fatalf("unexpired open should be skipped, got state=%v", st.state)
	}

	m.mu.Lock()
	m.states["dead"] = &connState{state: stateRetry, nextRetryAt: time.Now().Add(time.Hour)}
	m.mu.Unlock()
	m.reconnectOffline()

	m.mu.RLock()
	st = m.states["dead"]
	m.mu.RUnlock()
	if st.state != stateRetry {
		t.Fatalf("backing-off retry should be skipped, got state=%v", st.state)
	}
}

func TestHealth_ReportsState(t *testing.T) {
	m := NewManager()
	defer m.Close()

	m.mu.Lock()
	m.configs["s"] = model.ServerConfig{Name: "s", Transport: "stdio", Enabled: true}
	m.states["s"] = &connState{state: stateOpen, openUntil: time.Now().Add(time.Hour)}
	m.mu.Unlock()

	health := m.Health(nil)
	if len(health) != 1 {
		t.Fatalf("expected 1 health entry, got %d", len(health))
	}
	if health[0].State != string(stateOpen) {
		t.Fatalf("expected state %q, got %q", stateOpen, health[0].State)
	}
}
