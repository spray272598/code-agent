package contextx

import (
	"context"
	"fmt"

	"github.com/spray272598/code-agent/internal/types/common"
)

// Compressor multi-level history reduction (walicode-inspired L0–L3).
type Compressor struct {
	TokenBudget   int
	MessageBudget int
	KeepRecent    int
	// CompactThresholdRatio is the window-occupancy ratio that triggers a
	// warning/predictive pre-compact. Default 0.8 → warn at 80% of TokenBudget.
	CompactThresholdRatio float64
	Summarizer            *Summarizer
}

// DefaultCompactThresholdRatio warns at 80% of the token budget.
const DefaultCompactThresholdRatio = 0.8

func NewCompressor(tokenBudget int) *Compressor {
	if tokenBudget <= 0 {
		tokenBudget = 16000
	}
	return &Compressor{
		TokenBudget:           tokenBudget,
		MessageBudget:         50,
		KeepRecent:            8,
		CompactThresholdRatio: DefaultCompactThresholdRatio,
	}
}

// SetCompactThresholdRatio overrides the predictive pre-compact ratio.
// Clamped into (0,1]. Values outside are ignored (keeps the default).
func (c *Compressor) SetCompactThresholdRatio(ratio float64) {
	if ratio > 0 && ratio <= 1 {
		c.CompactThresholdRatio = ratio
	}
}

func (c *Compressor) SetSummarizer(s *Summarizer) { c.Summarizer = s }

func (c *Compressor) Needs(history []map[string]any) bool {
	if len(history) > c.MessageBudget {
		return true
	}
	return estimateHistory(history) > c.TokenBudget*4/5
}

// Pressure reports the current window occupancy ratio in [0,1+].
// >= CompactThresholdRatio means a predictive pre-compact is advised.
func (c *Compressor) Pressure(history []map[string]any) float64 {
	est := estimateHistory(history)
	if c.TokenBudget <= 0 {
		return 0
	}
	return float64(est) / float64(c.TokenBudget)
}

// ShouldPreCompact returns true when the window is at/above the configured
// compact threshold ratio but still below the hard budget (so a background
// summarize can run without blocking the response).
func (c *Compressor) ShouldPreCompact(history []map[string]any) bool {
	return c.Pressure(history) >= c.CompactThresholdRatio
}

// CompressResult includes optional summary for persistence.
type CompressResult struct {
	History []map[string]any
	Summary string
	Level   string // L0|L1|L2|L3
	Saved   int
}

// Compress L0 truncate + L1 priority/sliding. No LLM.
func (c *Compressor) Compress(history []map[string]any) (out []map[string]any, saved int) {
	r := c.CompressLevels(context.Background(), history, "", false)
	return r.History, r.Saved
}

// CompressLevels runs L0–L2 always when needed; L3 summary when useSummary and middle is large.
func (c *Compressor) CompressLevels(ctx context.Context, history []map[string]any, priorSummary string, useSummary bool) CompressResult {
	if len(history) == 0 {
		return CompressResult{History: history, Summary: priorSummary, Level: "none"}
	}
	before := estimateHistory(history)

	// L0: truncate long contents.
	// Use head-tail truncation so the trailing part (often the tool result's
	// conclusion/error) is kept instead of being silently dropped — a pure
	// head-only truncation risks losing exactly the most useful tail.
	trimmed := make([]map[string]any, len(history))
	for i, m := range history {
		cp := copyMap(m)
		if content, ok := cp["content"].(string); ok && len([]rune(content)) > common.CompressLongContentMaxRunes {
			cp["content"] = common.TruncateRunesKeepTail(content, common.CompressLongContentMaxRunes)
		}
		trimmed[i] = cp
	}
	level := "L0"
	if len(trimmed) <= c.KeepRecent {
		return CompressResult{History: trimmed, Summary: priorSummary, Level: level, Saved: before - estimateHistory(trimmed)}
	}

	// Split: middle vs recent
	cut := len(trimmed) - c.KeepRecent
	if cut < 0 {
		cut = 0
	}
	middle := trimmed[:cut]
	recent := trimmed[cut:]

	// L3: summarize middle when enabled and middle is non-trivial
	summary := priorSummary
	if useSummary && c.Summarizer != nil && len(middle) >= 4 {
		if s, err := c.Summarizer.Summarize(ctx, priorSummary, middle); err == nil && s != "" {
			summary = s
			level = "L3"
		}
	}

	// L1/L2: keep high-priority from middle + all recent
	keepMiddle := map[int]bool{}
	for i, m := range middle {
		if pri, ok := m["priority"].(int); ok && pri >= 5 {
			keepMiddle[i] = true
		}
		if role, _ := m["role"].(string); role == "tool" {
			if content, _ := m["content"].(string); containsErr(content) {
				keepMiddle[i] = true
			}
		}
	}
	var out []map[string]any
	if summary != "" {
		out = append(out, map[string]any{
			"role": "user", "content": "[SESSION_SUMMARY]\n" + summary,
			"priority": 5,
		})
		level = pickLevel(level, "L3")
	} else {
		// L1 keep selected middle messages
		for i, m := range middle {
			if keepMiddle[i] {
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			level = pickLevel(level, "L1")
		} else {
			level = pickLevel(level, "L2")
		}
	}
	out = append(out, recent...)

	// still over budget → hard recent only + summary
	if estimateHistory(out) > c.TokenBudget {
		out = recent
		if summary != "" {
			out = append([]map[string]any{{
				"role": "user", "content": "[SESSION_SUMMARY]\n" + summary, "priority": 5,
			}}, out...)
		}
		level = pickLevel(level, "L2")
	}
	return CompressResult{
		History: out, Summary: summary, Level: level,
		Saved: before - estimateHistory(out),
	}
}

func pickLevel(cur, next string) string {
	order := map[string]int{"": 0, "none": 0, "L0": 1, "L1": 2, "L2": 3, "L3": 4}
	if order[next] >= order[cur] {
		return next
	}
	return cur
}

func estimateHistory(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		if c, ok := m["content"].(string); ok {
			n += common.EstimateTokens(c)
		}
	}
	return n
}

func copyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func containsErr(s string) bool {
	return len(s) > 0 && (containsFold(s, "error") || containsFold(s, "失败") || containsFold(s, "failed") || containsFold(s, "DENIED"))
}

func containsFold(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Debug helper
var _ = fmt.Sprintf
