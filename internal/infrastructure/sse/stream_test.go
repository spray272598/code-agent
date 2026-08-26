package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUTF8SafeSlicer_ASCII(t *testing.T) {
	slicer := &UTF8SafeSlicer{}
	data := "Hello World"

	result, consumed := slicer.Slice(data, 5)
	if result != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", result)
	}
	if consumed != 5 {
		t.Errorf("expected consumed 5, got %d", consumed)
	}
}

func TestUTF8SafeSlicer_Multibyte(t *testing.T) {
	slicer := &UTF8SafeSlicer{}
	data := "你好世界"

	result, consumed := slicer.Slice(data, 5)
	if strings.Contains(result, string(data[4])) {
		t.Error("should not split multibyte character")
	}
	if consumed <= 0 {
		t.Error("should consume some bytes")
	}
}

func TestUTF8SafeSlicer_ShortData(t *testing.T) {
	slicer := &UTF8SafeSlicer{}
	data := "Hi"

	result, consumed := slicer.Slice(data, 100)
	if result != "Hi" {
		t.Errorf("expected 'Hi', got '%s'", result)
	}
	if consumed != 2 {
		t.Errorf("expected consumed 2, got %d", consumed)
	}
}

func TestUTF8SafeSlicer_ZeroMaxBytes(t *testing.T) {
	slicer := &UTF8SafeSlicer{}
	data := "Hello"

	result, consumed := slicer.Slice(data, 0)
	if result != "" {
		t.Errorf("expected empty result, got '%s'", result)
	}
	if consumed != 0 {
		t.Errorf("expected consumed 0, got %d", consumed)
	}
}

func TestByteBudget_AddBytes(t *testing.T) {
	budget := NewByteBudget(100)

	n, truncated := budget.AddString("hello")
	if n != 5 || truncated {
		t.Errorf("expected 5 bytes added, not truncated")
	}

	if budget.Used() != 5 {
		t.Errorf("expected 5 used, got %d", budget.Used())
	}
}

func TestByteBudget_Overflow(t *testing.T) {
	budget := NewByteBudget(10)

	budget.AddString("1234567890")

	n, truncated := budget.AddString("extra")
	if !truncated {
		t.Error("should be truncated")
	}
	if n > 10 {
		t.Errorf("should not exceed budget, got %d", n)
	}
}

func TestByteBudget_Reset(t *testing.T) {
	budget := NewByteBudget(50)

	budget.AddString("test")
	budget.Reset()

	if budget.Used() != 0 {
		t.Error("expected 0 after reset")
	}
	if budget.Truncated() {
		t.Error("expected truncated to be false after reset")
	}
}

func TestByteBudget_DefaultMax(t *testing.T) {
	budget := NewByteBudget(0)
	if budget.maxBytes != MaxStreamBytes {
		t.Errorf("expected default max %d, got %d", MaxStreamBytes, budget.maxBytes)
	}
}

func TestSSEStreamWriter_Headers(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)

	writer := NewSSEStreamWriter(w, flusher)
	SetSSEHeaders(writer.ResponseWriter())

	if ct := recorder.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", ct)
	}
	if recorder.Header().Get("Cache-Control") != "no-cache" {
		t.Error("expected no-cache")
	}
}

func TestSSEStreamWriter_WriteEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	ev := NewStructuredEvent(EventTextDelta, "sess-1", 1)
	ev.Delta = "Hello World"

	err := writer.WriteEvent(ev)
	if err != nil {
		t.Fatalf("WriteEvent failed: %v", err)
	}

	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "event: text_delta") {
		t.Errorf("missing event name in body: %s", body)
	}
	if !strings.Contains(body, "id: 1") {
		t.Errorf("missing event id in body: %s", body)
	}
	if !strings.Contains(body, "data:") {
		t.Errorf("missing data in body: %s", body)
	}
}

func TestSSEStreamWriter_NilEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	err := writer.WriteEvent(nil)
	if err == nil {
		t.Error("should error on nil event")
	}
}

func TestSSEStreamWriter_WriteComment(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	writer.WriteComment("ping 123")
	writer.Flush()

	body := recorder.Body.String()
	if !strings.Contains(body, ": ping 123") {
		t.Errorf("missing comment in body: %s", body)
	}
}

func TestSSEStreamWriter_SeqCounter(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	for i := 0; i < 10; i++ {
		ev := NewStructuredEvent(EventTextDelta, "sess", 0)
		writer.WriteEvent(ev)
	}

	if writer.Seq() != 10 {
		t.Errorf("expected seq 10, got %d", writer.Seq())
	}
}

func TestSetSSEHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	SetSSEHeaders(recorder)

	checks := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}

	for header, expected := range checks {
		if got := recorder.Header().Get(header); got != expected {
			t.Errorf("header %s: expected %s, got %s", header, expected, got)
		}
	}
}

func TestSSEContentType(t *testing.T) {
	if SSEContentType() != "text/event-stream" {
		t.Errorf("expected text/event-stream")
	}
}

func TestAdaptiveFlusher_StartStop(t *testing.T) {
	recorder := httptest.NewRecorder()
	var w http.ResponseWriter = recorder
	flusher, _ := w.(http.Flusher)
	writer := NewSSEStreamWriter(w, flusher)

	adaptiveFlusher := NewAdaptiveFlusher(writer, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	adaptiveFlusher.Start(ctx)

	ev := NewStructuredEvent(EventTextDelta, "sess", 1)
	writer.WriteEvent(ev)

	time.Sleep(150 * time.Millisecond)
	adaptiveFlusher.Stop()

	body := recorder.Body.String()
	if !strings.Contains(body, "event: text_delta") {
		t.Errorf("flushed event not found in body: %s", body)
	}
}
