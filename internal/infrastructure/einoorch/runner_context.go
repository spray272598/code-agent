package einoorch

// runner_context.go — history compression, summary persistence, and budget-aware context preparation.

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"

	"github.com/cloudwego/eino/schema"
)

// loadAndCompress loads message history, compresses when needed, and returns
// the final schema messages plus an optional compression note.
func (r *Runner) loadAndCompress(ctx context.Context, session *model.Session, userInput string, forceCompact bool, publish EventSink) ([]*schema.Message, string) {
	// always include current user at end
	if r.messages == nil {
		return []*schema.Message{schema.UserMessage(userInput)}, ""
	}
	// lazy: recent first
	recent, err := r.messages.ListAsMaps(ctx, session.ID, 24)
	if err != nil {
		observability.LogError("list recent messages", err)
	}
	full := recent
	needCompress := forceCompact || (r.compressor != nil && r.compressor.Needs(recent))
	if needCompress || len(recent) >= 24 {
		if more, err := r.messages.ListAsMaps(ctx, session.ID, 120); err == nil {
			full = more
		} else {
			observability.LogError("list full messages for compress", err)
		}
	}
	note := ""

	// --- ContextIntegrator path: budget-aware + memory-enriched compression ---
	if r.ctxIntegrator != nil {
		if r.hooks != nil {
			r.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
		}
		prior := ""
		if r.summaries != nil {
			var gerr error
			prior, gerr = r.summaries.Get(ctx, session.ID)
			if gerr != nil {
				observability.LogError("get session summary", gerr)
			}
		}

		// Wire MemoryEnricher if memory.Service is available and not already wired.
		if r.mem != nil && r.ctxIntegrator.SessionID() == "" {
			r.ctxIntegrator.SetSessionID(session.ID)
			r.ctxIntegrator.SetUserID(session.UserID)
		}

		opts := contextx.CompressOptions{
			Ctx:            ctx,
			PriorSummary:   prior,
			UseSummary:     forceCompact || len(full) > 16,
			SkipEnrichment: !forceCompact && len(full) <= 16,
		}
		enriched, cr := r.ctxIntegrator.Prepare(userInput, full, opts)
		full = enriched

		if cr.Level != "none" {
			if cr.Summary != "" && r.summaries != nil {
				if err := r.summaries.Save(ctx, session.ID, cr.Summary, common.EstimateTokens(cr.Summary)); err != nil {
					observability.LogError("save session summary", err)
				}
			}
			observability.Current().AddCompress(1)
			note = fmt.Sprintf("ci compress %s saved~%d", cr.Level, cr.Saved)
			if cr.Summary != "" {
				full = append([]map[string]any{{
					"role": "user", "content": "[SESSION_SUMMARY]\n" + cr.Summary, "priority": 5,
				}}, full...)
			}
		}
	} else if needCompress && r.compressor != nil {
		// --- Legacy compressor path (no ContextIntegrator) ---
		if r.hooks != nil {
			r.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
		}
		prior := ""
		if r.summaries != nil {
			var gerr error
			prior, gerr = r.summaries.Get(ctx, session.ID)
			if gerr != nil {
				observability.LogError("get session summary", gerr)
			}
		}
		// Async predictive compaction: when we are at/above the configured
		// threshold ratio but NOT forced and not yet over the hard budget,
		// run the LLM summary in a background goroutine and continue with the
		// fast deterministic L0–L2 compression so the response is not blocked.
		// The summary produced offline is saved and picked up on the next turn.
		if !forceCompact && r.compressor.ShouldPreCompact(full) && r.compressor.Summarizer != nil && len(full) > 16 {
			r.backgroundSummarize(ctx, session, full, prior)
			observability.Current().AddCompress(1)
			note = "background summarize scheduled (window >= threshold)"
		}
		useSum := forceCompact || len(full) > 16
		cr := r.compressor.CompressLevels(ctx, full, prior, useSum)
		full = cr.History
		if cr.Summary != "" && r.summaries != nil {
			if err := r.summaries.Save(ctx, session.ID, cr.Summary, common.EstimateTokens(cr.Summary)); err != nil {
				observability.LogError("save session summary", err)
			}
		}
		observability.Current().AddCompress(1)
		note = fmt.Sprintf("compress %s saved~%d", cr.Level, cr.Saved)
		if cr.Summary != "" {
			// inject summary as system-like user note
			full = append([]map[string]any{{
				"role": "user", "content": "[SESSION_SUMMARY]\n" + cr.Summary, "priority": 5,
			}}, full...)
		}
	}
	built := mapsToSchema(full)
	// ensure last message is current user input
	if len(built) == 0 {
		return []*schema.Message{schema.UserMessage(userInput)}, note
	}
	// drop trailing user if any and append current
	if built[len(built)-1].Role == schema.User {
		built = built[:len(built)-1]
	}
	built = append(built, schema.UserMessage(userInput))
	return built, note
}

// backgroundSummarize runs the LLM-based middle summary off the critical path.
// It reuses the existing Compressor.Summarizer and persists the resulting
// summary to the summary repository so the next turn picks it up. Failures are
// logged but never propagated — this is a best-effort latency optimization.
func (r *Runner) backgroundSummarize(ctx context.Context, session *model.Session, full []map[string]any, prior string) {
	if r.compressor == nil || r.compressor.Summarizer == nil {
		return
	}
	// capture only what we need; the parent ctx may be cancelled when the
	// response returns, so use a detached context with a bounded timeout.
	bctx := context.WithoutCancel(ctx)
	go func() {
		b, cancel := context.WithTimeout(bctx, 90*time.Second)
		defer cancel()
		s, err := r.compressor.Summarizer.Summarize(b, prior, full)
		if err != nil {
			observability.LogError("background summarize", err)
			return
		}
		if s == "" {
			return
		}
		if r.summaries != nil {
			if err := r.summaries.Save(b, session.ID, s, common.EstimateTokens(s)); err != nil {
				observability.LogError("save background summary", err)
			}
		}
		log.Printf("[info] async-summarize session=%s tokens~%d\n", session.ID, common.EstimateTokens(s))
	}()
}
