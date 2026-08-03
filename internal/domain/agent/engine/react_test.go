package engine

import (
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

func TestParseReActJSONOnly(t *testing.T) {
	s := ParseReAct(`{"name":"read_file","args":{"path":"a.go"}}`, nil)
	if len(s.Actions) != 1 || s.Actions[0].Name != "read_file" {
		t.Fatalf("actions=%+v", s.Actions)
	}
}

func TestParseReActThoughtAction(t *testing.T) {
	raw := `Thought: need to list go files
Action: {"name":"glob","args":{"pattern":"**/*.go"}}`
	s := ParseReAct(raw, nil)
	if s.Thought == "" || !containsSub(s.Thought, "list") {
		t.Fatalf("thought=%q", s.Thought)
	}
	if len(s.Actions) != 1 || s.Actions[0].Name != "glob" {
		t.Fatalf("actions=%+v", s.Actions)
	}
}

func TestParseReActFinalAnswer(t *testing.T) {
	raw := `Thought: done
Final Answer: all green`
	s := ParseReAct(raw, nil)
	if len(s.Actions) != 0 {
		t.Fatalf("want no actions, got %+v", s.Actions)
	}
	if s.FinalAnswer != "all green" {
		t.Fatalf("answer=%q", s.FinalAnswer)
	}
}

func TestParseReActNativeToolCalls(t *testing.T) {
	s := ParseReAct("Thought: x", []port.ToolCall{{Name: "bash", Args: map[string]any{"command": "ls"}}})
	if len(s.Actions) != 1 || s.Actions[0].Name != "bash" {
		t.Fatalf("%+v", s.Actions)
	}
}

func TestFormatObservation(t *testing.T) {
	o := FormatObservation("glob", "a.go\nb.go")
	if !containsSub(o, "Observation") || !containsSub(o, "glob") {
		t.Fatalf("%q", o)
	}
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
