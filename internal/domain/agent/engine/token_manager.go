package engine

import (
	"fmt"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/types/common"
)

// BudgetState is a point-in-time snapshot of the token budget.
type BudgetState struct {
	Total     int `json:"total"`
	Spent     int `json:"spent"`
	Reserved  int `json:"reserved"`
	Used      int `json:"used"` // Spent + Reserved
	Remaining int `json:"remaining"`
}

// TokenManager tracks budget pressure and mid-loop context trimming (extracted from Loop).
type TokenManager struct {
	Budget            int
	Spent             int
	Reserved          int
	DeterministicMode bool
	mu                sync.Mutex
}

func NewTokenManager(budget int) *TokenManager {
	if budget <= 0 {
		budget = DefaultTokenBudget
	}
	return &TokenManager{Budget: budget}
}

// Reserve atomically reserves n tokens. Returns false when remaining budget is insufficient.
func (t *TokenManager) Reserve(n int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n <= 0 {
		return true
	}
	if t.Spent+t.Reserved+n > t.Budget {
		return false
	}
	t.Reserved += n
	return true
}

// Release releases previously reserved tokens (capped at current Reserved).
func (t *TokenManager) Release(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n <= 0 {
		return
	}
	if n > t.Reserved {
		n = t.Reserved
	}
	t.Reserved -= n
}

// Commit converts reserved tokens into spent after successful execution.
func (t *TokenManager) Commit(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n <= 0 {
		return
	}
	if n > t.Reserved {
		n = t.Reserved
	}
	t.Reserved -= n
	t.Spent += n
}

// State returns a consistent snapshot of the budget.
func (t *TokenManager) State() BudgetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return BudgetState{
		Total:     t.Budget,
		Spent:     t.Spent,
		Reserved:  t.Reserved,
		Used:      t.Spent + t.Reserved,
		Remaining: t.Budget - t.Spent - t.Reserved,
	}
}

// Remaining returns the currently available tokens (budget - spent - reserved).
func (t *TokenManager) Remaining() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.Budget - t.Spent - t.Reserved
	if r < 0 {
		return 0
	}
	return r
}

// IsDeterministic reports whether the manager is in deterministic mode.
func (t *TokenManager) IsDeterministic() bool {
	return t.DeterministicMode
}

// Pressure reports whether used+context approaches budget.
func (t *TokenManager) Pressure(usedTokens int, msgs []port.ChatMessage, sys string) bool {
	return usedTokens+estimateMessageTokens(msgs, sys)+t.Reserved >= t.Budget
}

// Exhausted is hard stop when cumulative usage hits budget.
func (t *TokenManager) Exhausted(usedTokens int) bool {
	return usedTokens+t.Spent+t.Reserved >= t.Budget
}

// TrimMessages drops middle turns, keeps head + last n (emergency compress).
func (t *TokenManager) TrimMessages(messages []port.ChatMessage, keepTail int) []port.ChatMessage {
	if keepTail <= 0 {
		keepTail = DefaultKeepTailMessages
	}
	if len(messages) <= keepTail+2 {
		return messages
	}
	head := messages[:2]
	tail := messages[len(messages)-keepTail:]
	out := make([]port.ChatMessage, 0, 2+keepTail+1)
	out = append(out, port.ChatMessage{
		Role:    "user",
		Content: fmt.Sprintf("[TOKEN_BUDGET] Context trimmed mid-loop (budget=%d). Prefer Final Answer if possible.", t.Budget),
	})
	out = append(out, head...)
	out = append(out, tail...)
	return out
}

// Estimate estimates tokens for free text.
func (t *TokenManager) Estimate(s string) int {
	return common.EstimateTokens(s)
}
