package subagent

import (
	"context"
	"testing"
)

func TestGoalEvaluatorDecisionParsing(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    GoalEvaluatorDecision
		wantErr bool
	}{
		{"continue", "continue", GoalEvaluatorContinue, false},
		{"candidate_complete", "candidate_complete", GoalEvaluatorCandidateComplete, false},
		{"blocked", "blocked", GoalEvaluatorBlocked, false},
		{"case insensitive", "CONTINUE", GoalEvaluatorContinue, false},
		{"unknown", "achieved", GoalEvaluatorContinue, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGoalEvaluatorDecision(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseGoalEvaluatorDecision(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseGoalEvaluatorDecision(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGoalEvaluatorVerdictValidation(t *testing.T) {
	tests := []struct {
		name    string
		verdict GoalEvaluatorVerdict
		wantErr bool
	}{
		{
			name:    "valid continue",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorContinue, Evidence: "evidence", NextStep: "step", BlockerKey: ""},
			wantErr: false,
		},
		{
			name:    "valid candidate_complete",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorCandidateComplete, Evidence: "evidence", NextStep: "step", BlockerKey: ""},
			wantErr: false,
		},
		{
			name:    "valid blocked",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorBlocked, Evidence: "evidence", NextStep: "step", BlockerKey: "missing_access"},
			wantErr: false,
		},
		{
			name:    "empty evidence",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorContinue, Evidence: "", NextStep: "step", BlockerKey: ""},
			wantErr: true,
		},
		{
			name:    "empty next_step",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorContinue, Evidence: "evidence", NextStep: "", BlockerKey: ""},
			wantErr: true,
		},
		{
			name:    "blocked without blocker_key",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorBlocked, Evidence: "evidence", NextStep: "step", BlockerKey: ""},
			wantErr: true,
		},
		{
			name:    "blocked with invalid blocker_key",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorBlocked, Evidence: "evidence", NextStep: "step", BlockerKey: "Missing Access"},
			wantErr: true,
		},
		{
			name:    "continue with blocker_key",
			verdict: GoalEvaluatorVerdict{Decision: GoalEvaluatorContinue, Evidence: "evidence", NextStep: "step", BlockerKey: "key"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.verdict.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidBlockerKey(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"missing_access", true},
		{"infra_unavailable", true},
		{"permission_denied", true},
		{"123_valid", true},
		{"", false},
		{"MissingAccess", false},
		{"has-space", false},
		{"has.dot", false},
		{"has-dash", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := isValidBlockerKey(tt.key)
			if got != tt.valid {
				t.Errorf("isValidBlockerKey(%q) = %v, want %v", tt.key, got, tt.valid)
			}
		})
	}
}

func TestParseGoalEvaluatorVerdict(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    *GoalEvaluatorVerdict
		wantErr bool
	}{
		{
			name:    "valid continue",
			raw:     `{"decision":"continue","evidence":"work remains","next_step":"do X","blocker_key":""}`,
			want:    &GoalEvaluatorVerdict{Decision: GoalEvaluatorContinue, Evidence: "work remains", NextStep: "do X", BlockerKey: ""},
			wantErr: false,
		},
		{
			name:    "valid candidate_complete",
			raw:     `{"decision":"candidate_complete","evidence":"tests pass","next_step":"verify","blocker_key":""}`,
			want:    &GoalEvaluatorVerdict{Decision: GoalEvaluatorCandidateComplete, Evidence: "tests pass", NextStep: "verify", BlockerKey: ""},
			wantErr: false,
		},
		{
			name:    "valid blocked",
			raw:     `{"decision":"blocked","evidence":"no access","next_step":"request access","blocker_key":"missing_access"}`,
			want:    &GoalEvaluatorVerdict{Decision: GoalEvaluatorBlocked, Evidence: "no access", NextStep: "request access", BlockerKey: "missing_access"},
			wantErr: false,
		},
		{
			name:    "extra fields rejected",
			raw:     `{"decision":"continue","evidence":"x","next_step":"y","blocker_key":"","extra":true}`,
			wantErr: true,
		},
		{
			name:    "empty evidence rejected",
			raw:     `{"decision":"continue","evidence":" ","next_step":"y","blocker_key":""}`,
			wantErr: true,
		},
		{
			name:    "unknown decision rejected",
			raw:     `{"decision":"achieved","evidence":"x","next_step":"y","blocker_key":""}`,
			wantErr: true,
		},
		{
			name:    "blocked without key rejected",
			raw:     `{"decision":"blocked","evidence":"x","next_step":"y","blocker_key":""}`,
			wantErr: true,
		},
		{
			name:    "blocked with invalid key rejected",
			raw:     `{"decision":"blocked","evidence":"x","next_step":"y","blocker_key":"Missing Access"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGoalEvaluatorVerdict(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGoalEvaluatorVerdict() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.want != nil {
				if got.Decision != tt.want.Decision || got.Evidence != tt.want.Evidence ||
					got.NextStep != tt.want.NextStep || got.BlockerKey != tt.want.BlockerKey {
					t.Errorf("parseGoalEvaluatorVerdict() = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain JSON",
			input: `{"decision":"continue"}`,
			want:  `{"decision":"continue"}`,
		},
		{
			name:  "JSON in code block",
			input: "```json\n{\"decision\":\"continue\"}\n```",
			want:  `{"decision":"continue"}`,
		},
		{
			name:  "JSON with surrounding text",
			input: "Here is the result:\n{\"decision\":\"continue\"}\nEnd.",
			want:  `{"decision":"continue"}`,
		},
		{
			name:  "no JSON",
			input: "just text",
			want:  "just text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultEvaluator(t *testing.T) {
	e := &DefaultEvaluator{}
	ctx := context.Background()

	t.Run("completion signals", func(t *testing.T) {
		input := GoalEvaluatorInput{
			Objective:  "build feature",
			Transcript: "All tests pass. Compilation succeeded.",
		}
		verdict, err := e.Evaluate(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if verdict.Decision != GoalEvaluatorCandidateComplete {
			t.Errorf("expected candidate_complete, got %v", verdict.Decision)
		}
	})

	t.Run("blocker signals", func(t *testing.T) {
		input := GoalEvaluatorInput{
			Objective:  "deploy",
			Transcript: "Permission denied. Access denied.",
		}
		verdict, err := e.Evaluate(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if verdict.Decision != GoalEvaluatorBlocked {
			t.Errorf("expected blocked, got %v", verdict.Decision)
		}
		if verdict.BlockerKey != "permission_denied" {
			t.Errorf("expected permission_denied, got %s", verdict.BlockerKey)
		}
	})

	t.Run("infra signals", func(t *testing.T) {
		input := GoalEvaluatorInput{
			Objective:  "call API",
			Transcript: "Connection refused. Connection reset.",
		}
		verdict, err := e.Evaluate(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if verdict.Decision != GoalEvaluatorBlocked {
			t.Errorf("expected blocked, got %v", verdict.Decision)
		}
		if verdict.BlockerKey != "infra_unavailable" {
			t.Errorf("expected infra_unavailable, got %s", verdict.BlockerKey)
		}
	})

	t.Run("default continue", func(t *testing.T) {
		input := GoalEvaluatorInput{
			Objective:  "implement",
			Transcript: "Working on the task...",
		}
		verdict, err := e.Evaluate(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if verdict.Decision != GoalEvaluatorContinue {
			t.Errorf("expected continue, got %v", verdict.Decision)
		}
	})
}

func TestBoundedTranscript(t *testing.T) {
	t.Run("short transcript unchanged", func(t *testing.T) {
		input := "short text"
		got := boundedTranscript(input)
		if got != input {
			t.Errorf("expected unchanged, got %q", got)
		}
	})

	t.Run("long transcript truncated", func(t *testing.T) {
		input := make([]byte, transcriptMaxBytes+100)
		for i := range input {
			input[i] = 'x'
		}
		got := boundedTranscript(string(input))
		expectedSuffix := "\n... [truncated]"
		if len(got) != transcriptMaxBytes+len(expectedSuffix) {
			t.Errorf("expected %d bytes, got %d", transcriptMaxBytes+len(expectedSuffix), len(got))
		}
	})
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		text string
		subs []string
		want bool
	}{
		{text: "hello world", subs: []string{"hello"}, want: true},
		{text: "hello world", subs: []string{"missing"}, want: false},
		{text: "hello world", subs: []string{"missing", "hello"}, want: true},
		{text: "", subs: []string{"test"}, want: false},
	}

	for _, tt := range tests {
		got := containsAny(tt.text, tt.subs...)
		if got != tt.want {
			t.Errorf("containsAny(%q, %v) = %v, want %v", tt.text, tt.subs, got, tt.want)
		}
	}
}

func TestGoalEvaluatorJSONSchema(t *testing.T) {
	schema := EvaluatorJSONSchema()

	if schema["type"] != "object" {
		t.Error("schema type should be object")
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties should be a map")
	}

	decision, ok := props["decision"].(map[string]interface{})
	if !ok {
		t.Fatal("decision property should be a map")
	}

	enum, ok := decision["enum"].([]string)
	if !ok {
		t.Fatal("decision enum should be []string")
	}

	expected := map[string]bool{
		"continue":           false,
		"candidate_complete": false,
		"blocked":            false,
	}
	for _, v := range enum {
		if _, ok := expected[v]; ok {
			expected[v] = true
		}
	}
	for k, v := range expected {
		if !v {
			t.Errorf("enum missing %s", k)
		}
	}
}
