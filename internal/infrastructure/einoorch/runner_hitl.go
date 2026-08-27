package einoorch

// runner_hitl.go — human-in-the-loop resume, Eino graph interrupt detection, and HITL helpers.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"
)

// tryGraphResume re-enters the Eino graph at the interrupted tool node after HITL approve.
// Returns (result, true) when graph path handled the turn; (nil, false) to fall back.
func (r *Runner) tryGraphResume(
	ctx context.Context,
	session *model.Session,
	ready *security.AwaitingResume,
	interruptID string,
	publish EventSink,
	stats *runStats,
	opts engine.RunOptions,
) (*engine.Result, bool) {
	if r == nil || r.graphStore == nil || ready == nil || interruptID == "" {
		return nil, false
	}
	cm, err := r.newChatModel(ctx)
	if err != nil {
		return nil, false
	}
	cross := r.crossCut(session.UserID)
	einoTools := WrapRegistryCross(r.tools, r.perm, cross)
	handle, err := buildReactAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: einoTools},
		MaxStep:          r.cfg.MaxSteps,
		MessageModifier:  r.prompt.MessageModifier(ctx, session.UserID, session.ProjectID, "", nil, r.cfg.TokenBudget),
		GraphName:        DefaultGraphName,
	}, r.graphStore)
	if err != nil || handle == nil || handle.run == nil {
		return nil, false
	}

	info := ConfirmInfo{
		PendingID: ready.PermID, Tool: ready.Tool, Args: ready.Args,
		SessionID: session.ID,
	}
	// Mark interrupt target resumed with saved ConfirmInfo (GetInterruptState path).
	rctx := compose.ResumeWithData(ctx, interruptID, info)
	rctx = WithRunContext(rctx, RunContext{
		SessionID: session.ID, UserID: session.UserID, AutoApprove: true,
		Publish: publish, Cross: cross,
		EvalCollector: r.evalCollector,
	})
	cpID := DefaultGraphCheckPointID(session.ID)
	cbOpt := agentOptionsWithEval(publish, stats, session.ID, r.evalCollector)
	genOpts := append([]agent.AgentOption{cbOpt}, graphResumeOpts(cpID, false)...)

	t0 := time.Now()
	out, genErr := handle.Generate(rctx, nil, genOpts...)
	observability.Current().ObserveLLM(time.Since(t0))
	r.takeGraphInterrupt(session.ID)

	if genErr != nil {
		if isInterruptErr(genErr) {
			if iid := ExtractFirstInterruptID(genErr); iid != "" {
				r.setGraphInterrupt(session.ID, iid)
			}
			pending := firstPending(r.perm, session.ID)
			msg := "CONFIRM required — another approval needed (graph resume)"
			if pending != nil {
				msg = fmt.Sprintf("CONFIRM required tool=%s id=%s\nApprove then send 继续", pending.Tool, pending.ID)
				publish(&engine.Event{Type: engine.EventPermission, SubType: "confirm", Content: msg, Data: pending, Completed: true, Timestamp: nowMs()})
			}
			r.persistAssistant(ctx, session, msg)
			return &engine.Result{
				SessionID: session.ID, Response: msg, NeedPermission: true, Pending: pending,
				ErrorClass: "permission", ToolCalls: int(stats.toolCalls.Load()), TokenUsed: stats.TokenUsed(),
			}, true
		}
		// graph resume hard-failed → signal fallback
		publish(&engine.Event{Type: engine.EventThought, Content: "graph resume err: " + genErr.Error(), Timestamp: nowMs()})
		return nil, false
	}
	final := "done."
	if out != nil && strings.TrimSpace(out.Content) != "" {
		final = strings.TrimSpace(out.Content)
	}
	r.persistAssistant(ctx, session, final)
	tokenUsed := stats.TokenUsed()
	if tokenUsed <= 0 {
		tokenUsed = common.EstimateTokens(final)
	}
	if r.sessions != nil {
		session.AddTokens(tokenUsed)
		if err := r.sessions.Save(ctx, session); err != nil {
			observability.LogError("save session after graph resume", err)
		}
	}
	observability.Current().AddTokens(int64(tokenUsed))
	tc := int(stats.toolCalls.Load())
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino-graph-resume", "toolCalls": tc, "tokenEst": tokenUsed,
	}, Timestamp: nowMs()})
	return &engine.Result{
		SessionID: session.ID, Response: final, Steps: tc + 1, ToolCalls: tc, TokenUsed: tokenUsed,
	}, true
}

// isContinue detects user approval phrases (continue / ok / y).
func isContinue(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "继续" || s == "continue" || s == "ok" || s == "y" || s == "yes" || s == "继续执行"
}

// looksMulti detects team/parallel mode prefixes.
func looksMulti(s string) bool {
	ls := strings.ToLower(s)
	return strings.HasPrefix(ls, "/team") || strings.HasPrefix(ls, "/parallel") ||
		strings.Contains(ls, "parallel explore") || strings.Contains(ls, "team mode")
}

// looksDeep detects deep agent intent keywords.
func looksDeep(s string) bool {
	return deepagent.LooksDeep(s)
}

// firstPending returns the earliest pending confirmation for the session, or nil.
func firstPending(g *security.Guard, sessionID string) *security.PendingConfirm {
	if g == nil {
		return nil
	}
	list := g.ListPending(sessionID)
	if len(list) == 0 {
		return nil
	}
	return list[0]
}

// isInterruptErr detects Eino HITL / graph interrupt errors using public APIs only.
//
// Eino v0.9.x public surface:
//   - compose.ExtractInterruptInfo(err)  — graph interruptError / subGraphInterruptError
//   - compose.IsInterruptRerunError(err) — InterruptSignal / legacy interrupt-and-rerun
//
// There is no exported isInterruptError() or Interrupted interface; unexported types
// cannot be matched by name. See docs/eino-integration.md §Interrupt detection.
func isInterruptErr(err error) bool {
	if err == nil {
		return false
	}
	// 1) Typed graph interrupt (preferred)
	if _, ok := compose.ExtractInterruptInfo(err); ok {
		return true
	}
	// 2) Tool-level StatefulInterrupt / InterruptSignal / deprecated rerun
	if _, ok := compose.IsInterruptRerunError(err); ok {
		return true
	}
	// 3) Structural match: public GetInterruptContexts() on compose.InterruptCtx
	//    (covers wrapped providers if ExtractInterruptInfo missed them)
	type interruptCtxProvider interface {
		GetInterruptContexts() []*compose.InterruptCtx
	}
	var p interruptCtxProvider
	if errors.As(err, &p) {
		return true
	}
	// 4) Last-resort: known compose Error() prefixes only — never free-form "interrupted"
	msg := err.Error()
	if strings.HasPrefix(msg, interruptPrefixHappened) {
		return true
	}
	if strings.Contains(msg, interruptAndRerunMark) {
		return true
	}
	return false
}
