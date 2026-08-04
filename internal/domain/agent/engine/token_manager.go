package engine

import (
	"fmt"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/types/common"
)

// TokenManager tracks budget pressure and mid-loop context trimming (extracted from Loop).
type TokenManager struct {
	Budget int
}

func NewTokenManager(budget int) *TokenManager {
	if budget <= 0 {
		budget = DefaultTokenBudget
	}
	return &TokenManager{Budget: budget}
}

// Pressure reports whether used+context approaches budget.
func (t *TokenManager) Pressure(usedTokens int, msgs []port.ChatMessage, sys string) bool {
	return usedTokens+estimateMessageTokens(msgs, sys) >= t.Budget
}

// Exhausted is hard stop when cumulative usage hits budget.
func (t *TokenManager) Exhausted(usedTokens int) bool {
	return usedTokens >= t.Budget
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
		Role: "user",
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
