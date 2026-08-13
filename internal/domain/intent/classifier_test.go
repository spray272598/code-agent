package intent

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

func TestParseIntent(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`{"intent":"deep"}`, "deep"},
		{`Sure: {"intent":"team"}`, "team"},
		{`{"intent":"normal"}`, "normal"},
		{`{"intent": "DEEP"}`, "deep"},
		{`no json`, ""},
	}
	for _, tt := range tests {
		if got := parseIntent(tt.in); got != tt.want {
			t.Errorf("parseIntent(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

type fakeLLM struct {
	out string
	err error
}

func (f *fakeLLM) Generate(_ context.Context, _ *port.ChatRequest) (*port.ChatResponse, error) {
	return &port.ChatResponse{Content: f.out}, f.err
}
func (f *fakeLLM) GenerateStream(_ context.Context, _ *port.ChatRequest, _ func(port.StreamDelta)) (*port.ChatResponse, error) {
	return f.Generate(context.Background(), nil)
}

func TestClassifier_LLMFallback(t *testing.T) {
	c := NewClassifier(nil)
	c.SetLLM(&fakeLLM{out: `{"intent":"team"}`})
	r := c.Classify("帮我并行调研几个方案再汇总")
	if r.Intent != IntentTeam || r.Source != "llm" {
		t.Fatalf("expected llm team, got %s/%s", r.Intent, r.Source)
	}
	// no LLM → falls back to default normal
	c2 := NewClassifier(nil)
	if r := c2.Classify("帮我并行调研几个方案再汇总"); r.Intent != IntentNormal {
		t.Fatalf("expected normal without LLM, got %s", r.Intent)
	}
}

func TestClassifier_Continue(t *testing.T) {
	c := NewClassifier(nil)
	for _, input := range []string{"继续", "continue", "ok", "y", "yes", "继续执行"} {
		r := c.Classify(input)
		if r.Intent != IntentContinue {
			t.Fatalf("input %q: expected IntentContinue, got %s", input, r.Intent)
		}
	}
}

func TestClassifier_Deep(t *testing.T) {
	c := NewClassifier(nil)
	for _, input := range []string{"/deep add login", "deepagent: refactor auth", "deep agent: fix bug", "mode:deep implement"} {
		r := c.Classify(input)
		if r.Intent != IntentDeep {
			t.Fatalf("input %q: expected IntentDeep, got %s", input, r.Intent)
		}
		if r.CleanInput == "" {
			t.Fatalf("input %q: CleanInput should not be empty", input)
		}
	}
}

func TestClassifier_Team(t *testing.T) {
	c := NewClassifier(nil)
	tests := []struct {
		input       string
		expectClean string
	}{
		{"/team analyze architecture", "analyze architecture"},
		{"/parallel explore codebase", "explore codebase"},
	}
	for _, tt := range tests {
		r := c.Classify(tt.input)
		if r.Intent != IntentTeam {
			t.Fatalf("input %q: expected IntentTeam, got %s", tt.input, r.Intent)
		}
		if r.CleanInput != tt.expectClean {
			t.Fatalf("input %q: CleanInput=%q, expected %q", tt.input, r.CleanInput, tt.expectClean)
		}
	}
}

func TestClassifier_Normal(t *testing.T) {
	c := NewClassifier(nil)
	for _, input := range []string{"hello", "帮我写个函数", "what is this project about"} {
		r := c.Classify(input)
		if r.Intent != IntentNormal {
			t.Fatalf("input %q: expected IntentNormal, got %s", input, r.Intent)
		}
	}
}

func TestClassifier_Priority_Continue_Over_Deep(t *testing.T) {
	c := NewClassifier(nil)
	// "continue" 应该被识别为继续执行，而非其他意图
	r := c.Classify("continue")
	if r.Intent != IntentContinue {
		t.Fatalf("expected IntentContinue, got %s", r.Intent)
	}
}

func TestClassifier_Source(t *testing.T) {
	c := NewClassifier(nil)
	if r := c.Classify("/deep test"); r.Source != "prefix" {
		t.Fatalf("expected prefix, got %s", r.Source)
	}
	if r := c.Classify("hello"); r.Source != "default" {
		t.Fatalf("expected default, got %s", r.Source)
	}
}

func TestIntent_String(t *testing.T) {
	tests := []struct {
		intent Intent
		expect string
	}{
		{IntentNormal, "normal"},
		{IntentDeep, "deep"},
		{IntentTeam, "team"},
		{IntentContinue, "continue"},
	}
	for _, tt := range tests {
		if got := tt.intent.String(); got != tt.expect {
			t.Fatalf("expected %s, got %s", tt.expect, got)
		}
	}
}
