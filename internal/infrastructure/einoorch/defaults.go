package einoorch

import (
	"strings"
	"time"
)

// Named defaults for Eino Runner (avoid magic numbers in call sites).
const (
	DefaultMaxSteps    = 20
	DefaultTokenBudget = 32000
	DefaultModel       = "gpt-4o-mini"

	// Tool result cache
	DefaultToolCacheTTL  = 30 * time.Second
	DefaultToolCacheSize = 128

	// Context trim
	DefaultTrimKeepTail     = 16
	DefaultRewriterKeepTail = 14
	BudgetInputRatioNum     = 3
	BudgetInputRatioDen     = 4 // budget * 3/4

	// Event publish
	EventPublishTimeout = 300 * time.Millisecond

	// Multi-agent / deep sub-runs
	DefaultSubAgentMaxStep = 6
	SubAgentTimeoutBase    = 30 * time.Second
	SubAgentTimeoutPerStep = 15 * time.Second
	SubAgentTimeoutMax     = 180 * time.Second

	// Multi-agent budget (P0-2): token and agent quotas for parallel orchestration.
	DefaultMultiAgentTokenBudget = 16000
	DefaultMultiAgentAgentBudget = 4
	DefaultDeepAgentMaxSteps     = 20

	// Graph
	DefaultGraphName       = "CodeAgentReAct"
	DefaultGraphCheckpoint = "./data/eino-checkpoints"

	// UI/event truncation limits (chars)
	EventObservationMaxChars  = 800
	EventResultMaxChars       = 800
	ResumeObservationMaxChars = 1500
	ArgsRawMaxChars           = 400

	// Multi-agent / deep agent truncation limits (chars)
	SubAgentDoneMaxChars     = 120
	DeepGoalMaxChars         = 80
	DeepPhaseSummaryMaxChars = 2000
	DeepPhaseDoneMaxChars    = 100

	// Callback/event truncation limits (chars)
	StreamContentMaxChars = 200
	ArgsPreviewMaxChars   = 300
	ConfirmRespMaxChars   = 500
	DenyRespMaxChars      = 400
)

// Known compose interrupt error message prefixes (fallback when typed extract fails).
// Prefer compose.ExtractInterruptInfo / IsInterruptRerunError; these are last-resort only.
const (
	interruptPrefixHappened = "interrupt happened"
	interruptAndRerunMark   = "interrupt and rerun"
)

// modelContextWindow resolves a model id to its context window (tokens) for
// token-budget derivation. Mirrors the gateway mapping in the llm package.
// Returns 0 when the model is unknown so callers fall back to DefaultTokenBudget.
func modelContextWindow(model string) int {
	switch {
	case strings.Contains(model, "gpt-4o"), strings.Contains(model, "grok"), strings.Contains(model, "claude"):
		return 128000
	case strings.Contains(model, "gpt-4-turbo"):
		return 128000
	case strings.Contains(model, "gpt-4"):
		return 8192
	case strings.Contains(model, "gpt-3.5"):
		return 16385
	default:
		return 0
	}
}
