package engine

// Default agent loop knobs (shared by native Loop and config fallbacks).
const (
	DefaultMaxRounds   = 20
	DefaultTokenBudget = 32000
	// MaxToolResultChars truncates tool observations before model re-injection.
	MaxToolResultChars = 4000
	// DefaultKeepTailMessages is the emergency mid-loop history tail size.
	DefaultKeepTailMessages = 6
	// SSE publish timeouts
	SSEPublishSoftTimeoutMs     = 50
	SSEPublishCriticalTimeoutMs = 2000

	// LLM generation parameters
	DefaultTemperature   = 0.2
	ReflectMaxTokens     = 200
	DefaultLLMTimeoutSec = 120

	// Context and history defaults
	DefaultHistoryLimit     = 120
	DefaultCompactThreshold = 16
	BudgetPressureRatio     = 3 // budget * 1/3 triggers compress

	// Parallel tool execution
	MaxParallelToolCalls = 5

	// UI/event truncation limits (chars)
	EventObservationMaxChars = 800
	EventResultMaxChars      = 800
	EventAdvanceMaxChars     = 80
	AuditDetailMaxChars      = 300
	ReflectDetailMaxChars    = 200
)
