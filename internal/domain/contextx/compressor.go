package contextx

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/types/common"
)

// Compressor multi-level history reduction (walicode-inspired L0–L3).
//
// L0: Per-message long-content reduction with 4-level priority chain:
//
//	1. Semantic summary (LLM)  → SummarizeSingle() if Summarizer available
//	2. Segment-based sharding  → ShardLongText() preserves key paragraphs
//	3. Blob offload            → write to object store, return pointer
//	4. Head-tail truncation    → TruncateRunesKeepTail() as ultimate fallback
//
// L1: Priority-based middle message retention
// L2: Aggressive budget enforcement
// L3: LLM-based session summary
type Compressor struct {
	TokenBudget   int
	MessageBudget int
	KeepRecent    int
	// CompactThresholdRatio is the window-occupancy ratio that triggers a
	// warning/predictive pre-compact. Default 0.8 → warn at 80% of TokenBudget.
	CompactThresholdRatio float64
	Summarizer            *Summarizer
	BlobStore             blob.Store
	SessionID             string
	UserID                string
	// LongContentThresholdRunes overrides the default long-content threshold.
	// Default: common.CompressLongContentMaxRunes (2000).
	LongContentThresholdRunes int
	// MaxSummaryRunes caps the L0 semantic summary output. Default 400.
	MaxSummaryRunes int
}

// DefaultCompactThresholdRatio warns at 80% of the token budget.
const DefaultCompactThresholdRatio = 0.8

func NewCompressor(tokenBudget int) *Compressor {
	if tokenBudget <= 0 {
		tokenBudget = 16000
	}
	return &Compressor{
		TokenBudget:               tokenBudget,
		MessageBudget:             50,
		KeepRecent:                8,
		CompactThresholdRatio:     DefaultCompactThresholdRatio,
		LongContentThresholdRunes: common.CompressLongContentMaxRunes,
		MaxSummaryRunes:           400,
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
func (c *Compressor) SetBlobStore(s blob.Store)   { c.BlobStore = s }
func (c *Compressor) SetSessionID(id string)      { c.SessionID = id }
func (c *Compressor) SetUserID(id string)         { c.UserID = id }

// SetLongContentThreshold overrides the L0 long-content detection threshold.
func (c *Compressor) SetLongContentThreshold(runes int) {
	if runes > 500 {
		c.LongContentThresholdRunes = runes
	}
}

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

	// L0: reduce long contents with 4-level priority chain.
	trimmed := make([]map[string]any, len(history))
	for i, m := range history {
		cp := copyMap(m)
		if content, ok := cp["content"].(string); ok {
			runeLen := utf8.RuneCountInString(content)
			if runeLen > c.LongContentThresholdRunes {
				cp["content"] = c.processLongMessage(ctx, content, m)
			}
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
	// Use SelectSafeSplit to find a boundary that does not split tool pairs.
	// Estimate the token count of the intended "recent" section as target.
	if cut < len(trimmed) {
		recentTokens := estimateHistory(trimmed[cut:])
		safeCut := SelectSafeSplit(trimmed, recentTokens)
		if safeCut < cut {
			cut = safeCut
		}
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

// processLongMessage applies the 4-level priority chain to reduce a single
// long message's content. Returns the reduced content.
//
// Priority:
//  1. LLM semantic summary (if Summarizer available)
//  2. Segment-based sharding (always available, no LLM needed)
//  3. Blob offload (if BlobStore available)
//  4. Head-tail truncation (ultimate fallback)
func (c *Compressor) processLongMessage(ctx context.Context, content string, meta map[string]any) string {
	maxRunes := c.LongContentThresholdRunes
	if maxRunes <= 0 {
		maxRunes = common.CompressLongContentMaxRunes
	}

	// Level 1: Semantic summary via LLM
	if c.Summarizer != nil {
		if summary, err := c.Summarizer.SummarizeSingle(ctx, content, c.MaxSummaryRunes); err == nil && summary != "" {
			return summary
		}
		// LLM failed or returned empty → fall through to next level
		// Use rule-based summary as deterministic fallback
		if ruleSummary := RuleSummarizeSingle(content, c.MaxSummaryRunes); ruleSummary != "" {
			return ruleSummary
		}
	}

	// Level 2: Segment-based sharding (logical paragraph preservation)
	shardCfg := DefaultShardConfig()
	shardCfg.MaxRunes = maxRunes
	shardCfg.MaxSegments = 6
	if sharded := ShardLongText(content, shardCfg); sharded != content && hasShardMarkers(sharded) {
		return sharded
	}

	// Level 3: Blob offload (object store pointer)
	if c.BlobStore != nil {
		role, _ := meta["role"].(string)
		toolName, _ := meta["toolName"].(string)
		offloadKey := fmt.Sprintf("sessions/%s/history/%s-%s",
			safeStr(c.SessionID), safeStr(role), safeStr(toolName))
		if err := c.BlobStore.Put(ctx, offloadKey, []byte(content), "text/plain; charset=utf-8"); err == nil {
			preview := common.TruncateRunes(content, maxRunes/2)
			return preview + fmt.Sprintf(
				"\n\n[OFFLOADED: full content at object_key=%s; use storage API to fetch]",
				offloadKey)
		}
	}

	// Level 4: Ultimate fallback — head-tail truncation
	return common.TruncateRunesKeepTail(content, maxRunes)
}

func hasShardMarkers(s string) bool {
	if strings.Contains(s, "[SHARDED:") {
		return true
	}
	// truncateToBudget (used for single-segment fallback) also adds
	// "segments omitted" but does NOT preserve multiple segments.
	// Real sharding preserves head/tail segments separated by \n\n.
	return strings.Contains(s, "segments omitted") && strings.Contains(s, "\n\n")
}

func safeStr(s string) string {
	if s == "" {
		return "_"
	}
	for i := 0; i < len(s); i++ {
		if (s[i] < 'a' || s[i] > 'z') && (s[i] < 'A' || s[i] > 'Z') && (s[i] < '0' || s[i] > '9') && s[i] != '-' && s[i] != '_' {
			return "_"
		}
	}
	return s
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

// isToolRequest checks if a message is an assistant tool-request message.
// Tool requests are assistant messages that contain tool indicators such as
// "Action:", "tool_calls", or JSON-like content starting with '{'.
func isToolRequest(m map[string]any) bool {
	role, _ := m["role"].(string)
	if role != "assistant" {
		return false
	}
	content, _ := m["content"].(string)
	if strings.Contains(content, "Action:") ||
		strings.Contains(content, "tool_calls") ||
		strings.Contains(content, "{") {
		return true
	}
	return false
}

// isToolResult checks if a message is a tool result (role == "tool").
func isToolResult(m map[string]any) bool {
	role, _ := m["role"].(string)
	return role == "tool"
}

// findToolGroupEnd finds the end index of consecutive tool messages starting
// from startIdx. Returns the index after the last consecutive tool message.
func findToolGroupEnd(history []map[string]any, startIdx int) int {
	i := startIdx
	for i < len(history) && isToolResult(history[i]) {
		i++
	}
	return i
}

// SelectSafeSplit finds a safe split index for chat history that does not
// cut through tool-request/tool-result pairs.
//
// It accumulates token estimates from the newest to the oldest message until
// reaching targetTokens. If the resulting cut point lands on a tool-result
// message, it snaps backward to include all consecutive tool-result messages
// and the preceding assistant tool-request message in the same partition.
//
// Returns the safe split index where history[:idx] is the older "middle"
// section and history[idx:] is the newer "recent" section.
func SelectSafeSplit(history []map[string]any, targetTokens int) int {
	if len(history) == 0 {
		return 0
	}

	tokens := 0
	cut := 0
	for i := len(history) - 1; i >= 0; i-- {
		content, _ := history[i]["content"].(string)
		tokens += common.EstimateTokens(content)
		if tokens >= targetTokens {
			cut = i
			break
		}
	}

	if cut < len(history) && isToolResult(history[cut]) {
		snapCut := cut
		for snapCut > 0 && isToolResult(history[snapCut-1]) {
			snapCut--
		}
		if snapCut > 0 && isToolRequest(history[snapCut-1]) {
			snapCut--
		}
		cut = snapCut
	}

	return cut
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

var _ = fmt.Sprintf