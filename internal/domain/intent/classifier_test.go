package intent

import (
	"testing"
)

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
