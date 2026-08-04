package observability

import (
	"testing"
	"time"
)

func TestSetMetrics_injectable(t *testing.T) {
	orig := Current()
	defer SetMetrics(orig)

	m := NewMetrics()
	SetMetrics(m)
	Current().AddChatTotal(2)
	Current().AddToolCalls(1)
	Current().ObserveLLM(5 * time.Millisecond)
	snap := Current().Snapshot()
	if snap["chat_total"].(int64) != 2 {
		t.Fatalf("chat_total=%v", snap["chat_total"])
	}
	if snap["tool_calls"].(int64) != 1 {
		t.Fatalf("tool_calls=%v", snap["tool_calls"])
	}
	SetMetrics(nil) // restore default
	if Current() == nil {
		t.Fatal("nil metrics")
	}
}

func TestLogError_nilSafe(t *testing.T) {
	LogError("noop", nil)
}
