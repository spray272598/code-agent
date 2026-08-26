package sse

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHeartbeatManager(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	hm := NewHeartbeatManager(writer)
	if hm == nil {
		t.Fatal("NewHeartbeatManager returned nil")
	}

	if hm.CurrentInterval() != IdleHeartbeatInterval {
		t.Errorf("expected default interval %v, got %v", IdleHeartbeatInterval, hm.CurrentInterval())
	}
}

func TestHeartbeatManager_NotifyActivity(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	hm := NewHeartbeatManager(writer)
	initialActivity := hm.LastActivityTime()

	time.Sleep(10 * time.Millisecond)
	hm.NotifyActivity()

	afterActivity := hm.LastActivityTime()
	if afterActivity.Before(initialActivity) {
		t.Error("activity time should be updated")
	}
}

func TestHeartbeatManager_State(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	hm := NewHeartbeatManager(writer)
	if hm.State() != HeartbeatIdle {
		t.Errorf("expected initial state Idle, got %v", hm.State())
	}
}

func TestHeartbeatManager_StartStop(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	hm := NewHeartbeatManager(writer)
	hm.SetDynamicEnabled(false)
	hm.Start()
	time.Sleep(50 * time.Millisecond)

	hm.Stop()
	time.Sleep(20 * time.Millisecond)

	if hm.State() != HeartbeatClosed {
		t.Errorf("expected closed state, got %v", hm.State())
	}
}

func TestHeartbeatManager_SessionDuration(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	hm := NewHeartbeatManager(writer)
	time.Sleep(50 * time.Millisecond)

	duration := hm.SessionDuration()
	if duration < 50*time.Millisecond {
		t.Errorf("expected duration >= 50ms, got %v", duration)
	}
}

func TestHeartbeatManager_OnTimeout(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	hm := NewHeartbeatManager(writer)
	hm.timeout = 500 * time.Millisecond

	timeoutCalled := false
	hm.SetOnTimeout(func() {
		timeoutCalled = true
	})

	hm.Start()
	time.Sleep(1500 * time.Millisecond)
	hm.Stop()

	if !timeoutCalled {
		t.Error("timeout callback should have been called")
	}
}
