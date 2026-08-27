package contextx

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/types/common"
)

// BudgetState tracks the current token budget state with dynamic adjustment.
type BudgetState struct {
	TotalBudget     int       `json:"totalBudget"`
	InputBudget     int       `json:"inputBudget"`     // Max tokens for system + history
	OutputBudget    int       `json:"outputBudget"`    // Max tokens for LLM response
	UsedInput       int       `json:"usedInput"`       // History tokens used
	UsedOutput      int       `json:"usedOutput"`      // Output tokens (running estimate)
	AvailableTokens int       `json:"availableTokens"` // Total - UsedInput
	PressureRatio   float64   `json:"pressureRatio"`   // UsedInput / InputBudget
	LastUpdated     time.Time `json:"lastUpdated"`
}

// BudgetManager manages per-session token budgets with dynamic adjustment.
type BudgetManager struct {
	mu        sync.Mutex
	total     int
	input     int
	output    int
	realInput int // exact input tokens from provider usage; 0 = use heuristic estimate
}

// NewBudgetManager creates a BudgetManager with the given total token budget.
// Default split: 80% input (system+history), 20% output (LLM response).
func NewBudgetManager(totalTokens int) *BudgetManager {
	if totalTokens <= 0 {
		totalTokens = 16000
	}
	return &BudgetManager{
		total:  totalTokens,
		input:  int(float64(totalTokens) * 0.80),
		output: int(float64(totalTokens) * 0.20),
	}
}

// AdjustSplit changes the input/output ratio. Values are fractions of total.
// inputFrac + outputFrac should be <= 1.0.
func (m *BudgetManager) AdjustSplit(inputFrac, outputFrac float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inputFrac <= 0 || outputFrac <= 0 || inputFrac+outputFrac > 1.0 {
		return
	}
	m.input = int(float64(m.total) * inputFrac)
	m.output = int(float64(m.total) * outputFrac)
}

// Resize changes the total budget and re-adjusts input/output proportionally.
func (m *BudgetManager) Resize(newTotal int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if newTotal <= 0 {
		return
	}
	inputRatio := float64(m.input) / float64(m.total)
	outputRatio := float64(m.output) / float64(m.total)
	m.total = newTotal
	m.input = int(float64(newTotal) * inputRatio)
	m.output = int(float64(newTotal) * outputRatio)
}

// NewBudgetManagerForWindow derives a BudgetManager from a real model context
// window (used at 80%, matching the input/output split in NewBudgetManager).
// Pass 0 to fall back to the default 16000 budget.
func NewBudgetManagerForWindow(window int) *BudgetManager {
	if window <= 0 {
		return NewBudgetManager(0)
	}
	return NewBudgetManager(int(float64(window) * 0.80))
}

// SetContextWindow rebinds the budget to a real model context window while
// preserving the 80/20 input/output split. A non-positive window is ignored.
func (m *BudgetManager) SetContextWindow(window int) {
	if m == nil || window <= 0 {
		return
	}
	m.Resize(int(float64(window) * 0.80))
}

// SetRealInputTokens anchors the budget's input usage to the exact token count
// reported by the LLM provider (ChatResponse.PromptTokens), overriding the
// EstimateTokens heuristic. A non-positive value is ignored so the heuristic
// estimate remains the fallback. The latest real measurement wins.
func (m *BudgetManager) SetRealInputTokens(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.realInput = n
}

// Evaluate returns the current budget state given used input tokens.
func (m *BudgetManager) Evaluate(usedInputTokens int) BudgetState {
	m.mu.Lock()
	defer m.mu.Unlock()
	used := usedInputTokens
	if m.realInput > 0 {
		used = m.realInput
	}
	state := BudgetState{
		TotalBudget:     m.total,
		InputBudget:     m.input,
		OutputBudget:    m.output,
		UsedInput:       used,
		AvailableTokens: m.total - used,
		LastUpdated:     time.Now(),
	}
	if m.input > 0 {
		state.PressureRatio = float64(used) / float64(m.input)
	}
	if state.PressureRatio > 1.0 {
		state.PressureRatio = 1.0
	}
	return state
}

// ShouldCompress returns true when the history should be compressed.
// thresholdRatio: 0.0-1.0, triggers compression at this fraction of input budget.
func (m *BudgetManager) ShouldCompress(usedInputTokens int, thresholdRatio float64) bool {
	if thresholdRatio <= 0 || thresholdRatio >= 1.0 {
		thresholdRatio = 0.8
	}
	state := m.Evaluate(usedInputTokens)
	return state.PressureRatio >= thresholdRatio
}

// AllocateOutput returns the max output tokens based on remaining budget.
func (m *BudgetManager) AllocateOutput(usedInputTokens int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	remaining := m.total - usedInputTokens
	if remaining <= 0 {
		return 0
	}
	maxOutput := remaining / 4 // Reserve 75% for input
	if maxOutput > m.output {
		maxOutput = m.output
	}
	return maxOutput
}

// MemoryEnricher pre-fetches relevant memories before compression to enrich context.
type MemoryEnricher struct {
	// FetchRelated fetches related memory entries for the given query.
	// Returns (memoryEntries, totalTokens) for pre-pending to context.
	FetchRelated func(query string, sessionID string, maxTokens int) ([]string, int, error)
	// MaxMemoryTokens is the max tokens to allocate for memory enrichment.
	MaxMemoryTokens int
}

// NewMemoryEnricher creates a MemoryEnricher with default settings.
func NewMemoryEnricher(fetchFn func(query string, sessionID string, maxTokens int) ([]string, int, error)) *MemoryEnricher {
	return &MemoryEnricher{
		FetchRelated:    fetchFn,
		MaxMemoryTokens: 1000,
	}
}

// EnrichContext pre-fetches memories and returns enriched messages if budget allows.
func (e *MemoryEnricher) EnrichContext(query, sessionID string, history []map[string]any, budget BudgetState) ([]map[string]any, int) {
	if e == nil || e.FetchRelated == nil {
		return history, 0
	}
	maxTokens := e.MaxMemoryTokens
	if budget.AvailableTokens > 0 {
		remaining := budget.AvailableTokens - budget.OutputBudget
		if remaining > 0 && remaining < maxTokens {
			maxTokens = remaining
		}
	}
	if maxTokens <= 0 {
		return history, 0
	}

	entries, tokens, err := e.FetchRelated(query, sessionID, maxTokens)
	if err != nil || len(entries) == 0 {
		return history, 0
	}

	// Prepend memory entries as a system message.
	memContent := "## Relevant memories from past sessions:\n"
	for _, entry := range entries {
		memContent += "- " + entry + "\n"
	}
	memMsg := map[string]any{
		"role":              "system",
		"content":           memContent,
		"priority":          3,
		"memory_enrichment": true,
	}
	enriched := make([]map[string]any, 0, len(history)+1)
	enriched = append(enriched, memMsg)
	enriched = append(enriched, history...)
	return enriched, tokens
}

// ImportanceScore ranks messages for compression retention.
type ImportanceScore struct {
	Index      int     `json:"index"`
	Score      float64 `json:"score"`
	IsToolPair bool    `json:"isToolPair"`
	IsError    bool    `json:"isError"`
	IsSummary  bool    `json:"isSummary"`
}

// RankMessages returns importance scores for all messages in history.
// Higher scores = more important to keep during compression.
func RankMessages(history []map[string]any) []ImportanceScore {
	scores := make([]ImportanceScore, len(history))
	for i, m := range history {
		s := ImportanceScore{Index: i, Score: 0.5}

		role, _ := m["role"].(string)
		content, _ := m["content"].(string)

		// Role-based scoring.
		switch role {
		case "system":
			s.Score += 0.3 // System prompts are critical
		case "user":
			s.Score += 0.2 // User inputs matter
		case "tool":
			s.IsToolPair = true
			s.Score += 0.15 // Tool results matter
		case "assistant":
			if isToolRequest(m) {
				s.IsToolPair = true
				s.Score += 0.1
			} else {
				s.Score += 0.15 // Assistant responses
			}
		}

		// Content-based scoring.
		if containsErr(content) {
			s.IsError = true
			s.Score += 0.3 // Error messages are critical for debugging
		}
		if len(content) > 500 {
			s.Score += 0.1 // Longer messages tend to be more informative
		}
		if strings.Contains(content, "[SESSION_SUMMARY]") {
			s.IsSummary = true
			s.Score += 0.4 // Summaries are very important
		}

		scores[i] = s
	}
	return scores
}

// Throttle applies importance-based throttling: keeps high-score messages
// and drops low-score ones when over budget.
// Returns (kept messages, dropped count).
func Throttle(history []map[string]any, budget int) ([]map[string]any, int) {
	if len(history) == 0 {
		return nil, 0
	}
	scores := RankMessages(history)

	// Estimate token usage for each message.
	type indexedMsg struct {
		index  int
		msg    map[string]any
		score  float64
		tokens int
	}
	indexed := make([]indexedMsg, len(history))
	totalTokens := 0
	for i, m := range history {
		content, _ := m["content"].(string)
		tokens := estimateMsgTokens(content)
		indexed[i] = indexedMsg{
			index:  i,
			msg:    m,
			score:  scores[i].Score,
			tokens: tokens,
		}
		totalTokens += tokens
	}

	// If under budget, keep everything.
	if budget <= 0 || totalTokens <= budget {
		return history, 0
	}

	// Build keep mask: prioritize high-score messages, preserve tool pairs and order.
	keep := make([]bool, len(history))

	// Always keep system messages, user messages, and summaries (highest priority).
	for i, m := range history {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		switch role {
		case "system":
			keep[i] = true
		case "user":
			keep[i] = true
		case "assistant":
			if strings.Contains(content, "[SESSION_SUMMARY]") ||
				strings.Contains(content, "CONFIRM") ||
				strings.Contains(content, "Error") ||
				strings.Contains(content, "error") {
				keep[i] = true
			}
		}
	}

	// Greedily keep remaining messages by score, respecting order and tool pairs.
	usedTokens := 0
	for i := range history {
		if keep[i] {
			content, _ := history[i]["content"].(string)
			usedTokens += estimateMsgTokens(content)
		}
	}

	// Keep remaining messages in order if budget allows.
	for i := range history {
		if keep[i] {
			continue
		}
		content, _ := history[i]["content"].(string)
		msgTokens := estimateMsgTokens(content)
		if usedTokens+msgTokens <= budget {
			// Check if this is a tool result paired with a kept tool call.
			isPairedTool := false
			role, _ := history[i]["role"].(string)
			if role == "tool" {
				// Find preceding assistant message with tool request.
				for j := i - 1; j >= 0; j-- {
					pRole, _ := history[j]["role"].(string)
					if pRole == "assistant" && isToolRequest(history[j]) {
						if keep[j] {
							isPairedTool = true
						}
						break
					}
					if pRole == "tool" {
						break
					}
				}
			}
			// Keep if high score or paired with a kept tool call.
			if scores[i].Score >= 0.6 || isPairedTool {
				keep[i] = true
				usedTokens += msgTokens
			}
		}
	}

	// Build result preserving original order.
	var result []map[string]any
	dropped := 0
	for i, m := range history {
		if keep[i] {
			result = append(result, m)
		} else {
			dropped++
		}
	}
	return result, dropped
}

// estimateMsgTokens estimates the token count for a single message content.
// Delegates to the language-aware common.EstimateTokens so message-level and
// summary-level estimates share one heuristic (previously this used a separate
// len(content)/3 byte heuristic that disagreed with EstimateTokens).
func estimateMsgTokens(content string) int {
	if content == "" {
		return 0
	}
	return common.EstimateTokens(content)
}

// ContextIntegrator coordinates budget management, compression, and memory enrichment.
type ContextIntegrator struct {
	compressor *Compressor
	budgetMgr  *BudgetManager
	enricher   *MemoryEnricher
	sessionID  string
	userID     string
}

// NewContextIntegrator creates a coordinated context management system.
func NewContextIntegrator(compressor *Compressor, budgetMgr *BudgetManager, enricher *MemoryEnricher) *ContextIntegrator {
	return &ContextIntegrator{
		compressor: compressor,
		budgetMgr:  budgetMgr,
		enricher:   enricher,
	}
}

// SetSessionID sets the session ID for memory operations.
func (ci *ContextIntegrator) SetSessionID(id string) {
	ci.sessionID = id
	if ci.compressor != nil {
		ci.compressor.SetSessionID(id)
	}
}

// SessionID returns the current session ID.
func (ci *ContextIntegrator) SessionID() string {
	return ci.sessionID
}

// SetUserID sets the user ID.
func (ci *ContextIntegrator) SetUserID(id string) {
	ci.userID = id
	if ci.compressor != nil {
		ci.compressor.SetUserID(id)
	}
}

// SetMemoryEnricher injects a memory enricher for pre-fetching relevant memories.
func (ci *ContextIntegrator) SetMemoryEnricher(e *MemoryEnricher) {
	ci.enricher = e
}

// Prepare processes context before LLM call: enrich with memories, compress if needed.
// RecordLLMUsage anchors the session budget to exact provider token counts.
// Call this after each LLM Generate/GenerateStream with resp.PromptTokens /
// resp.OutputTokens so the compression decision uses ground truth instead of
// the EstimateTokens heuristic. Safe to call when integrator or its budget is nil.
func (ci *ContextIntegrator) RecordLLMUsage(promptTokens, outputTokens int) {
	if ci == nil || ci.budgetMgr == nil {
		return
	}
	ci.budgetMgr.SetRealInputTokens(promptTokens)
}

func (ci *ContextIntegrator) Prepare(query string, history []map[string]any, opts CompressOptions) ([]map[string]any, CompressResult) {
	result := CompressResult{History: history, Level: "none"}

	if ci.compressor == nil {
		return history, result
	}

	// Step 1: Check budget pressure.
	estimatedTokens := estimateHistory(history)
	state := ci.budgetMgr.Evaluate(estimatedTokens)

	// Step 2: Pre-fetch relevant memories if budget allows.
	if ci.enricher != nil && !opts.SkipEnrichment {
		history, _ = ci.enricher.EnrichContext(query, ci.sessionID, history, state)
	}

	// Step 3: Compress if needed.
	if ci.compressor.Needs(history) || ci.budgetMgr.ShouldCompress(estimateHistory(history), ci.compressor.CompactThresholdRatio) {
		result = ci.compressor.CompressLevels(opts.Ctx, history, opts.PriorSummary, opts.UseSummary)
	} else {
		result = CompressResult{History: history, Level: "none", Saved: 0}
	}

	return result.History, result
}

// CompressOptions controls compression behavior.
type CompressOptions struct {
	Ctx            context.Context
	PriorSummary   string
	UseSummary     bool
	SkipEnrichment bool
}

// Ensure strings import for the RankMessages function.
