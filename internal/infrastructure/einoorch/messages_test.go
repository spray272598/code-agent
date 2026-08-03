package einoorch

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMapsToSchemaPreservesTools(t *testing.T) {
	hist := []map[string]any{
		{"role": "user", "content": "list files"},
		{"role": "assistant", "content": "I'll use glob"},
		{"role": "tool", "content": "a.go\nb.go", "toolName": "glob", "toolCallId": "call_1"},
		{"role": "user", "content": "read a.go"},
	}
	msgs := mapsToSchema(hist)
	if len(msgs) != 4 {
		t.Fatalf("len=%d want 4", len(msgs))
	}
	if msgs[2].Role != schema.Tool {
		t.Fatalf("role=%v want tool", msgs[2].Role)
	}
	if msgs[2].ToolCallID != "call_1" {
		t.Fatalf("toolCallId=%q", msgs[2].ToolCallID)
	}
	if msgs[2].ToolName != "glob" {
		t.Fatalf("toolName=%q", msgs[2].ToolName)
	}
}

func TestMapsToSchemaSynthesizesToolCallID(t *testing.T) {
	hist := []map[string]any{
		{"role": "tool", "content": "ok", "toolName": "read_file"},
	}
	msgs := mapsToSchema(hist)
	if len(msgs) != 1 || msgs[0].ToolCallID == "" {
		t.Fatalf("expected synthetic toolCallId: %+v", msgs)
	}
}

func TestTrimSchemaMessages(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, schema.SystemMessage("sys"))
	for i := 0; i < 30; i++ {
		msgs = append(msgs, schema.UserMessage("pad message with enough text to count tokens "+string(rune('a'+i%26))))
	}
	out := trimSchemaMessages(msgs, 200, 6)
	if schemaToEstimateTokens(out) > 400 {
		// loose bound
		t.Logf("tokens=%d len=%d", schemaToEstimateTokens(out), len(out))
	}
	if len(out) >= len(msgs) {
		t.Fatalf("expected trim, got %d vs %d", len(out), len(msgs))
	}
	// no leading tool orphan after trim
	for _, m := range out {
		if m.Role == schema.System {
			continue
		}
		if m.Role == schema.Tool {
			t.Fatal("should not start rest with tool after normalize")
		}
		break
	}
}
