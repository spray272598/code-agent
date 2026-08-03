package engine

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/contextx"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
)

// HistoryLoader lazy-loads chat history: recent window first, full load only when compress needed.
type HistoryLoader struct {
	Messages    sessrepo.IMessageRepository
	Compressor  *contextx.Compressor
	RecentLimit int // default 24
	FullLimit   int // default 120
}

func NewHistoryLoader(msg sessrepo.IMessageRepository, comp *contextx.Compressor) *HistoryLoader {
	return &HistoryLoader{Messages: msg, Compressor: comp, RecentLimit: 24, FullLimit: 120}
}

// Load returns history maps and whether a full load was performed.
func (h *HistoryLoader) Load(ctx context.Context, sessionID string, forceCompact bool, messageCount int) (history []map[string]any, full bool, err error) {
	if h == nil || h.Messages == nil {
		return nil, false, nil
	}
	recentN := h.RecentLimit
	if recentN <= 0 {
		recentN = 24
	}
	fullN := h.FullLimit
	if fullN <= 0 {
		fullN = 120
	}

	// Always load recent window first (cheap path)
	recent, err := h.Messages.ListAsMaps(ctx, sessionID, recentN)
	if err != nil {
		return nil, false, err
	}

	needFull := forceCompact || messageCount > recentN
	if !needFull && h.Compressor != nil {
		needFull = h.Compressor.Needs(recent)
	}
	// if recent already full of messages at limit, load more for compress accuracy
	if !needFull && len(recent) >= recentN {
		needFull = true
	}
	if !needFull {
		return recent, false, nil
	}
	fullHist, err := h.Messages.ListAsMaps(ctx, sessionID, fullN)
	if err != nil {
		// fallback to recent
		return recent, false, nil
	}
	return fullHist, true, nil
}
