package eval

import (
	"testing"
	"time"
)

func TestCollectorBeginEndSession(t *testing.T) {
	cfg := DefaultEvalConfig()
	cfg.CollectSamples = true
	c := NewCollector(cfg)

	m := c.BeginSession("s1", "u1")
	if m == nil {
		t.Fatal("BeginSession returned nil")
	}
	if m.SessionID != "s1" {
		t.Errorf("session id mismatch: got %s", m.SessionID)
	}

	c.AddSample("s1", Sample{Dimension: DimEfficiency, Name: "tokens_total", Type: SampleCounter, Value: 5000})
	c.AddSample("s1", Sample{Dimension: DimEfficiency, Name: "step_count", Type: SampleCounter, Value: 5})
	c.AddSample("s1", Sample{Dimension: DimEfficiency, Name: "llm_calls", Type: SampleCounter, Value: 6})
	c.AddSample("s1", Sample{Dimension: DimToolUsage, Name: "tool_success", Type: SampleCounter, Value: 3})
	c.AddSample("s1", Sample{Dimension: DimSafety, Name: "permission_deny", Type: SampleCounter, Value: 1})
	c.SetTopology("s1", "deep")

	snap := c.GetSnapshot("s1")
	if snap == nil {
		t.Fatal("GetSnapshot returned nil")
	}
	if snap.TotalTokens != 5000 {
		t.Errorf("total tokens: got %d want 5000", snap.TotalTokens)
	}
	if snap.StepCount != 5 {
		t.Errorf("step count: got %d want 5", snap.StepCount)
	}
	if snap.ToolSuccesses != 3 {
		t.Errorf("tool successes: got %d want 3", snap.ToolSuccesses)
	}
	if snap.PermissionDenies != 1 {
		t.Errorf("permission denies: got %d want 1", snap.PermissionDenies)
	}
	if snap.Topology != "deep" {
		t.Errorf("topology: got %s want deep", snap.Topology)
	}

	m = c.EndSession("s1", true, "")
	if m == nil {
		t.Fatal("EndSession returned nil")
	}
	if !m.Completed {
		t.Error("session should be completed")
	}
	if !m.TaskCompleted {
		t.Error("task should be completed")
	}
	if m.DurationMs < 0 {
		t.Error("duration should be non-negative")
	}
	if m.EstimatedCostUSD <= 0 {
		t.Error("cost should be positive")
	}
	if c.ActiveSessions() != 0 {
		t.Errorf("active sessions: got %d want 0", c.ActiveSessions())
	}
}

func TestCollectorDisabled(t *testing.T) {
	c := NewCollector(EvalConfig{Enabled: false})
	if m := c.BeginSession("s1", "u1"); m != nil {
		t.Error("disabled collector should return nil")
	}
	c.AddSample("s1", Sample{Dimension: DimAccuracy, Value: 1})
	c.SetTopology("s1", "teams")
	if m := c.EndSession("s1", true, ""); m != nil {
		t.Error("disabled collector EndSession should return nil")
	}
	if snap := c.GetSnapshot("s1"); snap != nil {
		t.Error("disabled collector GetSnapshot should return nil")
	}
}

func TestCollectorToolBreakdown(t *testing.T) {
	c := NewCollector(DefaultEvalConfig())
	c.BeginSession("s1", "u1")
	c.AddToolBreakdown("s1", "bash", true)
	c.AddToolBreakdown("s1", "bash", true)
	c.AddToolBreakdown("s1", "read_file", false)
	c.AddToolBreakdown("s1", "grep", true)

	snap := c.GetSnapshot("s1")
	if snap.ToolSuccesses != 3 {
		t.Errorf("tool successes: got %d want 3", snap.ToolSuccesses)
	}
	if snap.ToolErrors != 1 {
		t.Errorf("tool errors: got %d want 1", snap.ToolErrors)
	}
	if snap.ToolBreakdown["bash"] != 2 {
		t.Errorf("bash breakdown: got %d want 2", snap.ToolBreakdown["bash"])
	}
	if snap.ToolBreakdown["read_file"] != 1 {
		t.Errorf("read_file breakdown: got %d want 1", snap.ToolBreakdown["read_file"])
	}
	if snap.ToolBreakdown["grep"] != 1 {
		t.Errorf("grep breakdown: got %d want 1", snap.ToolBreakdown["grep"])
	}
}

func TestGenerateReport(t *testing.T) {
	sessions := []SessionMetrics{
		{
			SessionID: "s1", StartedAt: time.Now(), EndedAt: time.Now().Add(30 * time.Second),
			DurationMs: 30000, Completed: true, TaskCompleted: true,
			TotalTokens: 4000, InputTokens: 2000, OutputTokens: 2000,
			StepCount: 4, ToolCallCount: 5, LLMCallCount: 5,
			ToolSuccesses: 5, ToolErrors: 0,
			MemoryHits: 3, MemoryMisses: 1, CompressionRatio: 0.5,
			Topology: "deep", EstimatedCostUSD: 0.02,
		},
		{
			SessionID: "s2", StartedAt: time.Now(), EndedAt: time.Now().Add(60 * time.Second),
			DurationMs: 60000, Completed: true, TaskCompleted: true,
			TotalTokens: 8000, InputTokens: 4000, OutputTokens: 4000,
			StepCount: 8, ToolCallCount: 10, LLMCallCount: 9,
			ToolSuccesses: 9, ToolErrors: 1, PermissionDenies: 2,
			MemoryHits: 5, MemoryMisses: 3, CompressionRatio: 0.3,
			Topology: "teams", EstimatedCostUSD: 0.04,
		},
		{
			SessionID: "s3", StartedAt: time.Now(), EndedAt: time.Now().Add(15 * time.Second),
			DurationMs: 15000, Completed: false, TaskFailed: true, ErrorClass: "timeout",
			TotalTokens: 2000, InputTokens: 1000, OutputTokens: 1000,
			StepCount: 2, ToolCallCount: 2, LLMCallCount: 3,
			ToolSuccesses: 1, ToolErrors: 1,
			MemoryHits: 1, MemoryMisses: 0, CompressionRatio: 0.7,
			Topology: "single", EstimatedCostUSD: 0.01,
		},
	}

	report := GenerateReport(sessions)
	if report.TotalSessions != 3 {
		t.Errorf("total sessions: got %d want 3", report.TotalSessions)
	}
	if report.CompletedSessions != 2 {
		t.Errorf("completed: got %d want 2", report.CompletedSessions)
	}
	if report.FailedSessions != 1 {
		t.Errorf("failed: got %d want 1", report.FailedSessions)
	}
	if report.TotalTokens != 14000 {
		t.Errorf("total tokens: got %d want 14000", report.TotalTokens)
	}
	if len(report.TopologyDistribution) != 3 {
		t.Errorf("topology distribution: got %d want 3", len(report.TopologyDistribution))
	}
	if report.Scores.Overall <= 0 {
		t.Error("overall score should be positive")
	}
	if report.Scores.Grade == "" {
		t.Error("grade should not be empty")
	}

	// Verify score dimensions are present.
	for _, dim := range []Dimension{DimAccuracy, DimEfficiency, DimSafety, DimToolUsage, DimContextQuality, DimCost} {
		if _, ok := report.Scores.Scores[dim]; !ok {
			t.Errorf("missing score for dimension: %s", dim)
		}
	}

	// Verify serialization works.
	json := report.ToJSON()
	if json == "{}" {
		t.Error("JSON serialization failed")
	}
	md := report.ToMarkdown()
	if len(md) == 0 {
		t.Error("Markdown serialization failed")
	}
}

func TestReportEmptySessions(t *testing.T) {
	report := GenerateReport(nil)
	if report.TotalSessions != 0 {
		t.Errorf("total sessions: got %d want 0", report.TotalSessions)
	}
	if report.Scores.Overall != 0 {
		t.Error("overall score should be 0 for empty sessions")
	}
}

func TestNormalizeFunctions(t *testing.T) {
	// Lower is better.
	if s := normalizeLowerIsBetter(0, 0, 100); s != 100 {
		t.Errorf("normalizeLowerIsBetter(0): got %f want 100", s)
	}
	if s := normalizeLowerIsBetter(100, 0, 100); s != 0 {
		t.Errorf("normalizeLowerIsBetter(100): got %f want 0", s)
	}
	if s := normalizeLowerIsBetter(50, 0, 100); s != 50 {
		t.Errorf("normalizeLowerIsBetter(50): got %f want 50", s)
	}

	// Higher is better.
	if s := normalizeHigherIsBetter(0, 0, 100); s != 0 {
		t.Errorf("normalizeHigherIsBetter(0): got %f want 0", s)
	}
	if s := normalizeHigherIsBetter(100, 0, 100); s != 100 {
		t.Errorf("normalizeHigherIsBetter(100): got %f want 100", s)
	}
	if s := normalizeHigherIsBetter(50, 0, 100); s != 50 {
		t.Errorf("normalizeHigherIsBetter(50): got %f want 50", s)
	}
}

func TestGradeFromScore(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{95, "A (Excellent)"},
		{85, "B (Good)"},
		{75, "C (Acceptable)"},
		{65, "D (Needs Improvement)"},
		{50, "F (Critical)"},
	}
	for _, tt := range tests {
		if got := gradeFromScore(tt.score); got != tt.want {
			t.Errorf("gradeFromScore(%f): got %s want %s", tt.score, got, tt.want)
		}
	}
}
