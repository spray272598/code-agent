package einoorch

// runner_persist.go — assistant message persistence to session history.

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"
)

// persistAssistant saves the assistant's final response text as a message.
func (r *Runner) persistAssistant(ctx context.Context, session *model.Session, text string) {
	if r.messages == nil {
		return
	}
	am := model.NewMessage(idMsg(), session.ID, "assistant", text)
	am.Priority = 3
	am.TokenCount = common.EstimateTokens(text)
	if err := r.messages.Save(ctx, am); err != nil {
		observability.LogError("save assistant message", err)
	}
}
