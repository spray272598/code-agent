package eval

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Report is a comprehensive evaluation report for a set of sessions.
type Report struct {
	GeneratedAt          time.Time        `json:"generatedAt"`
	TotalSessions        int              `json:"totalSessions"`
	CompletedSessions    int              `json:"completedSessions"`
	FailedSessions       int              `json:"failedSessions"`
	TotalDurationMs      int64            `json:"totalDurationMs"`
	TotalTokens          int              `json:"totalTokens"`
	TotalCostUSD         float64          `json:"totalCostUsd"`
	AvgLatencyMs         int64            `json:"avgLatencyMs"`
	ToolSuccessRate      float64          `json:"toolSuccessRate"`
	PermissionDenyRate   float64          `json:"permissionDenyRate"`
	TopologyDistribution map[string]int   `json:"topologyDistribution,omitempty"`
	Scores               ScoreBreakdown   `json:"scores"`
	Sessions             []SessionMetrics `json:"sessions,omitempty"`
}

// GenerateReport computes a Report from a set of session metrics.
func GenerateReport(sessions []SessionMetrics) Report {
	r := Report{
		GeneratedAt:   time.Now(),
		TotalSessions: len(sessions),
	}
	if len(sessions) == 0 {
		r.Scores = ScoreBreakdown{
			Scores:       map[Dimension]float64{},
			Explanations: map[Dimension]string{},
			Overall:      0,
			Grade:        "N/A",
		}
		return r
	}

	r.TopologyDistribution = map[string]int{}
	var totalLatency, totalTools, totalToolErrors int

	for _, s := range sessions {
		if s.Completed {
			r.CompletedSessions++
		} else {
			r.FailedSessions++
		}
		r.TotalDurationMs += s.DurationMs
		r.TotalTokens += s.TotalTokens
		r.TotalCostUSD += s.EstimatedCostUSD
		r.TopologyDistribution[s.Topology]++
		totalLatency += int(s.AvgLatencyMs)
		totalTools += s.ToolSuccesses + s.ToolErrors
		totalToolErrors += s.ToolErrors
	}

	if r.TotalSessions > 0 {
		r.AvgLatencyMs = int64(totalLatency) / int64(r.TotalSessions)
	}
	if totalTools > 0 {
		r.ToolSuccessRate = float64(totalTools-totalToolErrors) / float64(totalTools) * 100
	}

	// Compute scores.
	r.Scores = computeScores(sessions)
	return r
}

// computeScores calculates per-dimension scores and overall grade.
func computeScores(sessions []SessionMetrics) ScoreBreakdown {
	scores := map[Dimension]float64{}
	explanations := map[Dimension]string{}

	n := float64(len(sessions))
	if n == 0 {
		return ScoreBreakdown{Scores: scores, Explanations: explanations, Grade: "N/A"}
	}

	// Accuracy: percentage of completed tasks.
	completed := 0
	for _, s := range sessions {
		if s.Completed {
			completed++
		}
	}
	scores[DimAccuracy] = float64(completed) / n * 100
	explanations[DimAccuracy] = fmt.Sprintf("%.0f%% of sessions completed successfully (%d/%d)",
		scores[DimAccuracy], completed, len(sessions))

	// Efficiency: tokens per completed task (lower is better).
	var totalTokens, totalSteps float64
	for _, s := range sessions {
		totalTokens += float64(s.TotalTokens)
		totalSteps += float64(s.StepCount)
	}
	avgTokens := totalTokens / n
	avgSteps := totalSteps / n
	// Normalize: <4k tokens = 100, <8k = 80, <16k = 60, else = 40.
	effScore := normalizeLowerIsBetter(avgTokens, 4000, 16000)
	scores[DimEfficiency] = effScore
	explanations[DimEfficiency] = fmt.Sprintf("Avg %.0f tokens, %.1f steps/session (score: %.0f)",
		avgTokens, avgSteps, effScore)

	// Safety: inverse of permission deny rate.
	var totalDenies float64
	for _, s := range sessions {
		totalDenies += float64(s.PermissionDenies)
	}
	denyRate := totalDenies / n
	safetyScore := normalizeLowerIsBetter(denyRate, 0, 5)
	scores[DimSafety] = safetyScore
	explanations[DimSafety] = fmt.Sprintf("Avg %.1f permission denies/session (score: %.0f)",
		denyRate, safetyScore)

	// Tool Usage: success rate.
	var totalToolCalls, totalToolErrors float64
	for _, s := range sessions {
		totalToolCalls += float64(s.ToolSuccesses + s.ToolErrors)
		totalToolErrors += float64(s.ToolErrors)
	}
	var toolScore float64
	if totalToolCalls > 0 {
		toolScore = (totalToolCalls - totalToolErrors) / totalToolCalls * 100
	} else {
		toolScore = 100
	}
	scores[DimToolUsage] = toolScore
	explanations[DimToolUsage] = fmt.Sprintf("Tool success rate: %.1f%% (score: %.0f)",
		toolScore, toolScore)

	// Context Quality: memory hit rate + compression ratio.
	var totalHits, totalMisses float64
	var avgCompression float64
	for _, s := range sessions {
		totalHits += float64(s.MemoryHits)
		totalMisses += float64(s.MemoryMisses)
		avgCompression += s.CompressionRatio
	}
	memTotal := totalHits + totalMisses
	var memHitRate float64
	if memTotal > 0 {
		memHitRate = totalHits / memTotal * 100
	}
	avgCompression /= n
	ctxScore := memHitRate*0.6 + normalizeHigherIsBetter(avgCompression, 0.3, 0.8)*0.4
	scores[DimContextQuality] = ctxScore
	explanations[DimContextQuality] = fmt.Sprintf("Memory hit: %.0f%%, avg compression: %.0f%% (score: %.0f)",
		memHitRate, avgCompression*100, ctxScore)

	// Cost: lower is better.
	var avgCost float64
	for _, s := range sessions {
		avgCost += s.EstimatedCostUSD
	}
	avgCost /= n
	costScore := normalizeLowerIsBetter(avgCost, 0.01, 0.10)
	scores[DimCost] = costScore
	explanations[DimCost] = fmt.Sprintf("Avg $%.4f/session (score: %.0f)", avgCost, costScore)

	// Overall: weighted average.
	overall := weightedScore(scores)
	return ScoreBreakdown{
		Scores:       scores,
		Explanations: explanations,
		Overall:      overall,
		Grade:        gradeFromScore(overall),
	}
}

// weightedScore computes overall from dimension weights.
// Accuracy 30%, Efficiency 25%, Safety 15%, ToolUsage 15%, ContextQuality 10%, Cost 5%.
func weightedScore(scores map[Dimension]float64) float64 {
	weights := map[Dimension]float64{
		DimAccuracy:       0.30,
		DimEfficiency:     0.25,
		DimSafety:         0.15,
		DimToolUsage:      0.15,
		DimContextQuality: 0.10,
		DimCost:           0.05,
	}
	var total float64
	for dim, w := range weights {
		total += scores[dim] * w
	}
	return total
}

func gradeFromScore(score float64) string {
	switch {
	case score >= 90:
		return "A (Excellent)"
	case score >= 80:
		return "B (Good)"
	case score >= 70:
		return "C (Acceptable)"
	case score >= 60:
		return "D (Needs Improvement)"
	default:
		return "F (Critical)"
	}
}

// normalizeLowerIsBetter maps a value where lower is better to a 0-100 score.
func normalizeLowerIsBetter(val, min, max float64) float64 {
	if val <= min {
		return 100
	}
	if val >= max {
		return 0
	}
	return 100 * (max - val) / (max - min)
}

// normalizeHigherIsBetter maps a value where higher is better to a 0-100 score.
func normalizeHigherIsBetter(val, min, max float64) float64 {
	if val <= min {
		return 0
	}
	if val >= max {
		return 100
	}
	return 100 * (val - min) / (max - min)
}

// ToJSON serializes the report to pretty JSON.
func (r Report) ToJSON() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ToMarkdown renders the report as a Markdown summary.
func (r Report) ToMarkdown() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Agent Evaluation Report\n\n"))
	b.WriteString(fmt.Sprintf("**Generated**: %s\n\n", r.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("| Metric | Value |\n|--------|-------|\n"))
	b.WriteString(fmt.Sprintf("| Total Sessions | %d |\n", r.TotalSessions))
	b.WriteString(fmt.Sprintf("| Completed | %d |\n", r.CompletedSessions))
	b.WriteString(fmt.Sprintf("| Failed | %d |\n", r.FailedSessions))
	b.WriteString(fmt.Sprintf("| Total Duration | %s |\n", time.Duration(r.TotalDurationMs)*time.Millisecond))
	b.WriteString(fmt.Sprintf("| Total Tokens | %d |\n", r.TotalTokens))
	b.WriteString(fmt.Sprintf("| Avg Latency | %dms |\n", r.AvgLatencyMs))
	b.WriteString(fmt.Sprintf("| Tool Success Rate | %.1f%% |\n", r.ToolSuccessRate))
	b.WriteString(fmt.Sprintf("| Total Cost | $%.4f |\n\n", r.TotalCostUSD))

	b.WriteString("## Dimension Scores\n\n")
	b.WriteString("| Dimension | Score | Explanation |\n|-----------|-------|-------------|\n")
	for _, dim := range []Dimension{DimAccuracy, DimEfficiency, DimSafety, DimToolUsage, DimContextQuality, DimCost} {
		score := r.Scores.Scores[dim]
		expl := r.Scores.Explanations[dim]
		b.WriteString(fmt.Sprintf("| %s | %.0f | %s |\n", dim, score, expl))
	}
	b.WriteString(fmt.Sprintf("\n**Overall**: %.0f / 100 (Grade: %s)\n", r.Scores.Overall, r.Scores.Grade))

	if len(r.TopologyDistribution) > 0 {
		b.WriteString("\n## Topology Distribution\n\n")
		for topo, count := range r.TopologyDistribution {
			b.WriteString(fmt.Sprintf("- %s: %d sessions\n", topo, count))
		}
	}
	return b.String()
}

// Compile-time checks.
var (
	_ = GenerateReport(nil)
	_ = func(r Report) string { return r.ToJSON() }
	_ = func(r Report) string { return r.ToMarkdown() }
)
