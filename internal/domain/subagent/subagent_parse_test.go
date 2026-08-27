package subagent

import (
	"testing"
)

func TestParseSpecsSinglePrompt(t *testing.T) {
	input := map[string]any{"prompt": "do X"}
	specs := parseSpecs(input)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	if specs[0].Prompt != "do X" {
		t.Errorf("expected prompt='do X', got %q", specs[0].Prompt)
	}
}

func TestParseSpecsTasksArray(t *testing.T) {
	input := map[string]any{
		"tasks": []any{
			map[string]any{"prompt": "task a"},
			map[string]any{"prompt": "task b", "role": "explore"},
		},
	}
	specs := parseSpecs(input)
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	if specs[0].Prompt != "task a" {
		t.Errorf("expected first prompt='task a', got %q", specs[0].Prompt)
	}
	if specs[1].Role != "explore" {
		t.Errorf("expected second role='explore', got %q", specs[1].Role)
	}
}

func TestParseSpecsEmptyTasks(t *testing.T) {
	input := map[string]any{"tasks": []any{}}
	specs := parseSpecs(input)
	if len(specs) != 0 {
		t.Errorf("expected 0 specs, got %d", len(specs))
	}
}

func TestParseSpecsWithMaxStepsAndTools(t *testing.T) {
	input := map[string]any{
		"tasks": []any{
			map[string]any{
				"prompt":   "task",
				"maxSteps": float64(5),
				"tools":    []any{"bash", "grep"},
			},
		},
	}
	specs := parseSpecs(input)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	if specs[0].MaxSteps != 5 {
		t.Errorf("expected maxSteps=5, got %d", specs[0].MaxSteps)
	}
	if len(specs[0].Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(specs[0].Tools))
	}
}

func TestParseSpecsNilInput(t *testing.T) {
	specs := parseSpecs(nil)
	if len(specs) != 0 {
		t.Errorf("expected 0 specs for nil input, got %d", len(specs))
	}
}

func TestParseSpecsEmptyInput(t *testing.T) {
	specs := parseSpecs(map[string]any{})
	if len(specs) != 0 {
		t.Errorf("expected 0 specs for empty input, got %d", len(specs))
	}
}

func TestFormatResultsEmpty(t *testing.T) {
	result := FormatResults(nil)
	if result == "" {
		t.Error("expected non-empty string for nil results")
	}
}

func TestFormatResultsSingle(t *testing.T) {
	results := []Result{
		{ID: "sa-1", Role: "explore", Status: "ok", Output: "found 3 files", Steps: 5, Duration: 1234},
	}
	result := FormatResults(results)
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !containsStr(result, "sa-1") {
		t.Error("missing ID in result")
	}
	if !containsStr(result, "found 3 files") {
		t.Error("missing output in result")
	}
}

func TestFormatResultsMultiple(t *testing.T) {
	results := []Result{
		{ID: "sa-1", Role: "explore", Status: "ok", Output: "result1"},
		{ID: "sa-2", Role: "verify", Status: "error", Output: "failed"},
	}
	result := FormatResults(results)
	if !containsStr(result, "sa-1") || !containsStr(result, "sa-2") {
		t.Error("missing results in output")
	}
}

func TestFormatResultsWithSummary(t *testing.T) {
	results := []Result{
		{ID: "sa-1", Role: "explore", Status: "ok", Output: "long output here", Summary: "short summary"},
	}
	result := FormatResults(results)
	if !containsStr(result, "short summary") {
		t.Error("expected summary in output")
	}
	if containsStr(result, "long output here") {
		t.Error("full output should be omitted when summary exists")
	}
}

func containsStr(s, substr string) bool {
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
