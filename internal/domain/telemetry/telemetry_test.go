package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestNopDefault(t *testing.T) {
	Set(nil)
	// must not panic
	IncChatError()
	IncToolCall()
	IncPermissionDeny()
	IncMemoryWrite()
	IncMemoryRead()
	IncBlobOffload()
	IncCompress()
	IncReflect()
	AddTokens(3)
	ObserveLLM(time.Millisecond)
	ObserveTool(time.Millisecond)
	TraceEvent(map[string]any{"k": 1})
	Warnf("w %s", "x")
	Errorf("e %s", "y")
	ctx, end := StartSpan(context.Background(), "t", map[string]string{"a": "b"})
	if ctx == nil || end == nil {
		t.Fatal("nil")
	}
	end.End()
	if err := SpanTool(context.Background(), "echo", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

type countingSink struct {
	Nop
	n int
}

func (c *countingSink) IncToolCall() { c.n++ }

func TestSetCustomSink(t *testing.T) {
	c := &countingSink{}
	Set(c)
	defer Set(nil)
	IncToolCall()
	IncToolCall()
	if c.n != 2 {
		t.Fatalf("n=%d", c.n)
	}
	if Current() == nil {
		t.Fatal("current")
	}
}
