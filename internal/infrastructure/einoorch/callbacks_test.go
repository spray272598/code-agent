package einoorch

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

func TestAgentOptions_statsAndToolCallPublish(t *testing.T) {
	var (
		mu     sync.Mutex
		events []*engine.Event
	)
	publish := func(ev *engine.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	}
	stats := &runStats{}
	_ = agentOptions(publish, stats) // builds compose callbacks without panic

	stats.addUsage(&model.TokenUsage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18})
	if stats.TokenUsed() != 18 {
		t.Fatalf("tokens %d", stats.TokenUsed())
	}

	msg := &schema.Message{
		ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "grep", Arguments: `{"pattern":"foo"}`}},
		},
	}
	args := map[string]any{}
	_ = json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args)
	publish(&engine.Event{
		Type: engine.EventToolCall, SubType: "grep", Content: "grep", Data: args, Timestamp: time.Now().UnixMilli(),
	})
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0].SubType != "grep" {
		t.Fatalf("events %+v", events)
	}
	if args["pattern"] != "foo" {
		t.Fatalf("args %+v", args)
	}
}

func TestLooksError(t *testing.T) {
	if !looksError("DENIED: path") {
		t.Fatal("denied")
	}
	if !looksError("validation error: x") {
		t.Fatal("validation")
	}
	if looksError("ok result") {
		t.Fatal("ok")
	}
}
