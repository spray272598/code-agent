package plan

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// BudgetConfig configures the agent-call budget system.
type BudgetConfig struct {
	MaxAgentCalls   int           `json:"max_agent_calls"`   // max agent calls per session (0 = unlimited)
	MaxTurns        int           `json:"max_turns"`         // max turns per session (0 = unlimited)
	MaxTokensPerTurn int          `json:"max_tokens_per_turn"` // max tokens per turn (0 = unlimited)
	ResetInterval   time.Duration `json:"reset_interval"`    // how often to reset budget (0 = never)
}

// DefaultBudgetConfig returns sensible defaults.
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		MaxAgentCalls:   128,
		MaxTurns:        0, // unlimited
		MaxTokensPerTurn: 0, // unlimited
		ResetInterval:   0, // never
	}
}

// BudgetState tracks the current budget consumption.
type BudgetState struct {
	AgentCallsUsed  int       `json:"agent_calls_used"`
	TurnsUsed       int       `json:"turns_used"`
	TokensUsed      int       `json:"tokens_used"`
	Reserved        int       `json:"reserved"` // reserved agent calls for subagents
	LastReset       time.Time `json:"last_reset"`
}

// BudgetManager controls agent-call and turn budgets.
type BudgetManager struct {
	mu     sync.RWMutex
	config BudgetConfig
	state  BudgetState
}

// NewBudgetManager creates a new budget manager.
func NewBudgetManager(config BudgetConfig) *BudgetManager {
	return &BudgetManager{
		config: config,
		state: BudgetState{
			LastReset: time.Now(),
		},
	}
}

// CanConsumeAgentCall checks if an agent call is allowed within budget.
func (bm *BudgetManager) CanConsumeAgentCall() (bool, string) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if bm.config.MaxAgentCalls <= 0 {
		return true, "" // unlimited
	}

	available := bm.config.MaxAgentCalls - bm.state.AgentCallsUsed - bm.state.Reserved
	if available <= 0 {
		return false, fmt.Sprintf("agent call budget exhausted (%d/%d used, %d reserved)",
			bm.state.AgentCallsUsed, bm.config.MaxAgentCalls, bm.state.Reserved)
	}
	return true, ""
}

// ConsumeAgentCall records an agent call consumption.
func (bm *BudgetManager) ConsumeAgentCall() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.config.MaxAgentCalls > 0 && bm.state.AgentCallsUsed >= bm.config.MaxAgentCalls {
		return fmt.Errorf("agent call budget exhausted")
	}
	bm.state.AgentCallsUsed++
	return nil
}

// ReserveAgentCalls reserves agent calls for subagents.
func (bm *BudgetManager) ReserveAgentCalls(count int) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.config.MaxAgentCalls > 0 {
		available := bm.config.MaxAgentCalls - bm.state.AgentCallsUsed - bm.state.Reserved
		if count > available {
			return fmt.Errorf("cannot reserve %d agent calls: only %d available", count, available)
		}
	}
	bm.state.Reserved += count
	return nil
}

// ReleaseReserved releases reserved agent calls (e.g., when subagent completes).
func (bm *BudgetManager) ReleaseReserved(count int) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.state.Reserved -= count
	if bm.state.Reserved < 0 {
		bm.state.Reserved = 0
	}
}

// CanConsumeTurn checks if a turn is allowed within budget.
func (bm *BudgetManager) CanConsumeTurn() (bool, string) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if bm.config.MaxTurns <= 0 {
		return true, "" // unlimited
	}

	if bm.state.TurnsUsed >= bm.config.MaxTurns {
		return false, fmt.Sprintf("turn budget exhausted (%d/%d used)", bm.state.TurnsUsed, bm.config.MaxTurns)
	}
	return true, ""
}

// ConsumeTurn records a turn consumption.
func (bm *BudgetManager) ConsumeTurn() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.config.MaxTurns > 0 && bm.state.TurnsUsed >= bm.config.MaxTurns {
		return fmt.Errorf("turn budget exhausted")
	}
	bm.state.TurnsUsed++
	return nil
}

// ConsumeTokens records token consumption.
func (bm *BudgetManager) ConsumeTokens(count int) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.state.TokensUsed += count
}

// Remaining returns the remaining budget for agent calls.
func (bm *BudgetManager) Remaining() int {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if bm.config.MaxAgentCalls <= 0 {
		return -1 // unlimited
	}
	remaining := bm.config.MaxAgentCalls - bm.state.AgentCallsUsed - bm.state.Reserved
	if remaining < 0 {
		return 0
	}
	return remaining
}

// State returns a snapshot of the current budget state.
func (bm *BudgetManager) State() BudgetState {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.state
}

// Reset resets the budget state.
func (bm *BudgetManager) Reset() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.state = BudgetState{
		LastReset: time.Now(),
	}
}

// BudgetSummary returns a summary string for prompt injection.
func (bm *BudgetManager) BudgetSummary() string {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if bm.config.MaxAgentCalls <= 0 && bm.config.MaxTurns <= 0 {
		return "" // no budget constraints
	}

	var b strings.Builder
	b.WriteString("## Budget\n")
	if bm.config.MaxAgentCalls > 0 {
		b.WriteString(fmt.Sprintf("- Agent calls: %d/%d used", bm.state.AgentCallsUsed, bm.config.MaxAgentCalls))
		if bm.state.Reserved > 0 {
			b.WriteString(fmt.Sprintf(" (%d reserved)", bm.state.Reserved))
		}
		b.WriteString("\n")
	}
	if bm.config.MaxTurns > 0 {
		b.WriteString(fmt.Sprintf("- Turns: %d/%d used\n", bm.state.TurnsUsed, bm.config.MaxTurns))
	}
	return b.String()
}

// CheckAndConsume is an atomic check-and-consume for agent calls.
// Returns true if the call was allowed and consumed, false otherwise.
func (bm *BudgetManager) CheckAndConsume() (bool, string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.config.MaxAgentCalls > 0 {
		available := bm.config.MaxAgentCalls - bm.state.AgentCallsUsed - bm.state.Reserved
		if available <= 0 {
			return false, fmt.Sprintf("agent call budget exhausted (%d/%d used)",
				bm.state.AgentCallsUsed, bm.config.MaxAgentCalls)
		}
	}
	bm.state.AgentCallsUsed++
	return true, ""
}
