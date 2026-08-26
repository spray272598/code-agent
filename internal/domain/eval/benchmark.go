package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// BenchmarkScenario defines a single benchmark scenario for regression testing.
type BenchmarkScenario struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Input            string `json:"input"`
	ExpectedTopology string `json:"expectedTopology,omitempty"`
	MaxTokens        int    `json:"maxTokens,omitempty"`
	MaxSteps         int    `json:"maxSteps,omitempty"`
}

// BenchmarkSuite is a collection of benchmark scenarios.
type BenchmarkSuite struct {
	Name      string              `json:"name"`
	Scenarios []BenchmarkScenario `json:"scenarios"`
	CreatedAt time.Time           `json:"createdAt"`
}

// BenchmarkResult captures the result of running a benchmark scenario.
type BenchmarkResult struct {
	ScenarioID     string          `json:"scenarioId"`
	SessionMetrics *SessionMetrics `json:"sessionMetrics"`
	Score          float64         `json:"score"`
	Passed         bool            `json:"passed"`
	Notes          string          `json:"notes,omitempty"`
	DurationMs     int64           `json:"durationMs"`
}

// BenchmarkReport summarizes a benchmark run.
type BenchmarkReport struct {
	Suite        BenchmarkSuite    `json:"suite"`
	Results      []BenchmarkResult `json:"results"`
	OverallScore float64           `json:"overallScore"`
	PassRate     float64           `json:"passRate"`
	GeneratedAt  time.Time         `json:"generatedAt"`
}

// DefaultBenchmarkSuite returns the default set of benchmark scenarios.
func DefaultBenchmarkSuite() BenchmarkSuite {
	return BenchmarkSuite{
		Name: "code-agent-default",
		Scenarios: []BenchmarkScenario{
			{
				ID:          "simple_query",
				Name:        "Simple Query",
				Description: "A straightforward question that should use single agent.",
				Input:       "What is the capital of France?",
			},
			{
				ID:               "deep_implementation",
				Name:             "Deep Implementation",
				Description:      "A complex coding task requiring plan-act-reflect.",
				Input:            "Implement a binary search tree with insertion, deletion, and traversal methods in Go.",
				ExpectedTopology: "deep",
			},
			{
				ID:               "parallel_research",
				Name:             "Parallel Research",
				Description:      "A task that benefits from parallel multi-agent exploration.",
				Input:            "Compare the performance of PostgreSQL and MySQL for high-concurrency OLTP workloads, and also analyze their indexing strategies.",
				ExpectedTopology: "teams",
			},
			{
				ID:               "multi_step_bugfix",
				Name:             "Multi-step Bug Fix",
				Description:      "Debug a complex issue requiring investigation and fixing.",
				Input:            "The login API returns 500 errors intermittently. Investigate the root cause, fix the issue, and write tests.",
				ExpectedTopology: "deep",
			},
			{
				ID:               "code_refactor",
				Name:             "Code Refactoring",
				Description:      "Refactor a module with clear plan and execution steps.",
				Input:            "Refactor the authentication module to use JWT instead of sessions, update all related tests, and document the migration.",
				ExpectedTopology: "deep",
			},
		},
		CreatedAt: time.Now(),
	}
}

// NewBenchmarkSuite creates a benchmark suite with custom scenarios.
func NewBenchmarkSuite(name string, scenarios []BenchmarkScenario) BenchmarkSuite {
	return BenchmarkSuite{
		Name:      name,
		Scenarios: scenarios,
		CreatedAt: time.Now(),
	}
}

// EvaluateScores compares two sets of scores and returns improvement notes.
func EvaluateScores(baseline, current ScoreBreakdown) map[Dimension]string {
	comparison := map[Dimension]string{}
	for dim, currentScore := range current.Scores {
		baselineScore, exists := baseline.Scores[dim]
		if !exists {
			comparison[dim] = fmt.Sprintf("NEW: %.1f", currentScore)
			continue
		}
		diff := currentScore - baselineScore
		arrow := "→"
		if diff > 2 {
			arrow = "↑"
		} else if diff < -2 {
			arrow = "↓"
		}
		comparison[dim] = fmt.Sprintf("%.1f %s %.1f (Δ%+.1f)", baselineScore, arrow, currentScore, diff)
	}
	return comparison
}

// SaveReport writes a benchmark report to a JSON file.
func SaveReport(report BenchmarkReport, path string) error {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// LoadReport reads a benchmark report from a JSON file.
func LoadReport(path string) (*BenchmarkReport, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var report BenchmarkReport
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, fmt.Errorf("unmarshal report: %w", err)
	}
	return &report, nil
}

// CompareReports compares two benchmark reports and returns improvement analysis.
func CompareReports(baseline, current BenchmarkReport) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Benchmark Comparison: %s vs %s\n\n",
		baseline.GeneratedAt.Format(time.RFC3339), current.GeneratedAt.Format(time.RFC3339)))

	b.WriteString(fmt.Sprintf("| Metric | Baseline | Current | Delta |\n"))
	b.WriteString(fmt.Sprintf("|--------|----------|---------|-------|\n"))
	b.WriteString(fmt.Sprintf("| Overall Score | %.1f | %.1f | %+.1f |\n",
		baseline.OverallScore, current.OverallScore, current.OverallScore-baseline.OverallScore))
	b.WriteString(fmt.Sprintf("| Pass Rate | %.1f%% | %.1f%% | %+.1f%% |\n",
		baseline.PassRate, current.PassRate, current.PassRate-baseline.PassRate))
	b.WriteString(fmt.Sprintf("| Total Scenarios | %d | %d | %+d |\n",
		len(baseline.Results), len(current.Results), len(current.Results)-len(baseline.Results)))

	b.WriteString("\n## Per-Scenario Comparison\n\n")
	b.WriteString("| Scenario | Baseline Score | Current Score | Status |\n")
	b.WriteString("|----------|---------------|---------------|--------|\n")
	for i := range current.Results {
		if i < len(baseline.Results) {
			base := baseline.Results[i]
			curr := current.Results[i]
			status := "→"
			if curr.Score > base.Score+2 {
				status = "↑ IMPROVED"
			} else if curr.Score < base.Score-2 {
				status = "↓ REGRESSED"
			}
			b.WriteString(fmt.Sprintf("| %s | %.1f | %.1f | %s |\n",
				curr.ScenarioID, base.Score, curr.Score, status))
		}
	}

	return b.String()
}

// Compile-time checks.
var (
	_ = DefaultBenchmarkSuite()
	_ = func() error { return SaveReport(BenchmarkReport{}, "bench.json") }
	_ = func() (*BenchmarkReport, error) { return LoadReport("bench.json") }
)
