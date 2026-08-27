package engine

import (
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

func TestParseToolCallsEmpty(t *testing.T) {
	if calls := parseToolCalls(""); calls != nil {
		t.Errorf("expected nil, got %v", calls)
	}
	if calls := parseToolCalls("   "); calls != nil {
		t.Errorf("expected nil for whitespace, got %v", calls)
	}
}

func TestParseToolCallsSingle(t *testing.T) {
	input := `{"name":"bash","args":{"command":"echo hello"}}`
	calls := parseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Errorf("expected name=bash, got %s", calls[0].Name)
	}
	if calls[0].Args["command"] != "echo hello" {
		t.Errorf("expected command=echo hello, got %v", calls[0].Args["command"])
	}
}

func TestParseToolCallsArray(t *testing.T) {
	input := `[{"name":"bash","args":{"command":"ls"}},{"name":"grep","args":{"pattern":"foo"}}]`
	calls := parseToolCalls(input)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "bash" || calls[1].Name != "grep" {
		t.Errorf("unexpected names: %v, %v", calls[0].Name, calls[1].Name)
	}
}

func TestParseToolCallsCodeBlock(t *testing.T) {
	input := "Here is the tool call:\n```json\n{\"name\":\"read_file\",\"args\":{\"path\":\"src/main.go\"}}\n```"
	calls := parseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Errorf("expected read_file, got %s", calls[0].Name)
	}
}

func TestParseToolCallsCodeBlockNoLang(t *testing.T) {
	input := "```\n{\"name\":\"grep\",\"args\":{\"pattern\":\"error\"}}\n```"
	calls := parseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "grep" {
		t.Errorf("expected grep, got %s", calls[0].Name)
	}
}

func TestParseToolCallsNestedJSON(t *testing.T) {
	input := "I'll search for the file: {\"name\":\"glob\",\"args\":{\"pattern\":\"**/*.go\"}}"
	calls := parseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "glob" {
		t.Errorf("expected glob, got %s", calls[0].Name)
	}
}

func TestParseToolCallsNoArgs(t *testing.T) {
	input := `{"name":"bash","args":null}`
	calls := parseToolCalls(input)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Args == nil {
		t.Error("expected empty map, got nil")
	}
}

func TestParseToolCallsInvalidJSON(t *testing.T) {
	input := "not json at all"
	calls := parseToolCalls(input)
	if calls != nil {
		t.Errorf("expected nil, got %v", calls)
	}
}

func TestParseToolCallsEmptyName(t *testing.T) {
	input := `[{"name":"","args":{}},{"name":"bash","args":{}}]`
	calls := parseToolCalls(input)
	// The first call has empty name, should be filtered
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (empty name filtered), got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Errorf("expected bash, got %s", calls[0].Name)
	}
}

func TestIsToolFail(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Error: file not found", true},
		{"task failed", true},
		{"文件不存在", true},
		{"not found", true},
		{"tool not found: bash", true},
		{"DENIED by policy", true},
		{"operation completed successfully", false},
		{"file contents here", false},
	}
	for _, tt := range tests {
		got := isToolFail(tt.input)
		if got != tt.want {
			t.Errorf("isToolFail(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFormatTools(t *testing.T) {
	tools := []map[string]string{
		{"name": "bash", "description": "Run shell commands"},
		{"name": "read_file", "description": "Read file contents"},
	}
	result := formatTools(tools)
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !contains(result, "bash") || !contains(result, "read_file") {
		t.Errorf("missing tool names in output: %s", result)
	}
}

func TestFormatToolsEmpty(t *testing.T) {
	result := formatTools(nil)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestEnsureID(t *testing.T) {
	tc := port.ToolCall{Name: "bash", Args: map[string]any{"command": "echo hi"}}
	id := ensureID(tc)
	if id == "" {
		t.Error("expected non-empty ID")
	}
	// Second call with same data should produce same ID
	id2 := ensureID(tc)
	if id != id2 {
		t.Errorf("expected same ID, got %q and %q", id, id2)
	}
}

func TestEnsureIDPreservesExisting(t *testing.T) {
	tc := port.ToolCall{ID: "custom-id", Name: "bash", Args: map[string]any{}}
	id := ensureID(tc)
	if id != "custom-id" {
		t.Errorf("expected custom-id, got %s", id)
	}
}

func TestToolSig(t *testing.T) {
	calls := []port.ToolCall{
		{Name: "bash", Args: map[string]any{"command": "ls"}},
	}
	sig1 := toolSig(calls)
	sig2 := toolSig(calls)
	if sig1 == "" {
		t.Error("expected non-empty sig")
	}
	if sig1 != sig2 {
		t.Error("expected stable sig")
	}
	// Different input should produce different sig
	calls2 := []port.ToolCall{
		{Name: "bash", Args: map[string]any{"command": "rm -rf /"}},
	}
	sig3 := toolSig(calls2)
	if sig1 == sig3 {
		t.Error("expected different sig for different input")
	}
}

func TestToolsFingerprint(t *testing.T) {
	reg := tool.NewRegistry()
	fp := toolsFingerprint(reg)
	if fp != "" {
		t.Errorf("expected empty fingerprint for empty registry, got %q", fp)
	}
}

func TestToolsFingerprintNil(t *testing.T) {
	fp := toolsFingerprint(nil)
	if fp != "" {
		t.Errorf("expected empty for nil, got %q", fp)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
