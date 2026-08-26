package einoorch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/intent"
	"github.com/spray272598/code-agent/internal/domain/model"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"
)

// Config for Eino-backed orchestration.
type Config struct {
	APIKey      string
	APIBase     string
	Model       string
	MaxSteps    int
	ByAzure     bool
	APIVersion  string
	UseStream   bool
	TokenBudget int // context token budget for compress + guard
	// GraphResume enables Eino CheckPointStore + ResumeWithData for in-graph HITL.
	// When false, only app-level Guard awaiting resume is used.
	GraphResume bool
	// GraphCheckPointDir is the durable store dir (empty → memory store when GraphResume).
	GraphCheckPointDir string
	// Router enables M3/3.1 multi-model routing: when non-nil, the model
	// endpoint is selected per intent before each model call.
	Router *model.Router
	// CompactThresholdRatio triggers a background predictive summarize at this
	// window occupancy ratio (0,1]. Default 0.8.
	CompactThresholdRatio float64
}

// Runner uses CloudWeGo Eino ReAct; security/business cross-cuts stay in domain tools.
type Runner struct {
	cfg          Config
	tools        *tool.MapRegistry
	perm         *security.Guard
	sessions     sessrepo.ISessionRepository
	messages     sessrepo.IMessageRepository
	summaries    sessrepo.ISummaryRepository
	hooks        *hook.Bus
	audit        audit.Repository
	cache        *tool.ResultCache
	compressor   *contextx.Compressor
	prompt       *PromptBuilder
	skills       *skill.Service
	mem          *memory.Service
	persona      string
	Multi        *MultiAgent
	tokens       *engine.TokenManager
	intentRouter intent.Router

	// optional in-graph interrupt resume
	graphStore compose.CheckPointStore
	graphMu    sync.Mutex
	// sessionID → last interrupt context id (process-local; durable via graph store)
	graphIntr map[string]string
}

func NewRunner(
	cfg Config,
	tools *tool.MapRegistry,
	perm *security.Guard,
	sessions sessrepo.ISessionRepository,
	messages sessrepo.IMessageRepository,
) *Runner {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = DefaultTokenBudget
	}
	if cfg.GraphCheckPointDir == "" {
		cfg.GraphCheckPointDir = DefaultGraphCheckpoint
	}
	comp := contextx.NewCompressor(cfg.TokenBudget / 2)
	if cfg.CompactThresholdRatio > 0 {
		comp.SetCompactThresholdRatio(cfg.CompactThresholdRatio)
	}
	r := &Runner{
		cfg: cfg, tools: tools, perm: perm,
		sessions: sessions, messages: messages,
		persona:    defaultPersona(),
		cache:      tool.NewResultCache(DefaultToolCacheTTL, DefaultToolCacheSize),
		compressor: comp,
		prompt:     NewPromptBuilder(NewPromptContext(), tools),
		tokens:     engine.NewTokenManager(cfg.TokenBudget),
		graphIntr:  make(map[string]string),
	}
	if cfg.GraphResume {
		if st, err := NewFileCheckPointStore(cfg.GraphCheckPointDir); err == nil {
			r.graphStore = st
		} else {
			observability.LogError("eino checkpoint store file", err)
			r.graphStore = NewMemoryCheckPointStore()
		}
	}
	r.Multi = NewMultiAgent(r)
	return r
}

func (r *Runner) SetHooks(h *hook.Bus)                         { r.hooks = h }
func (r *Runner) SetAudit(a audit.Repository)                  { r.audit = a }
func (r *Runner) SetSummaryRepo(s sessrepo.ISummaryRepository) { r.summaries = s }
func (r *Runner) SetSkills(s *skill.Service) {
	r.skills = s
	if r.prompt != nil {
		r.prompt.SetSkills(s)
	}
}
func (r *Runner) SetMemory(m *memory.Service) {
	r.mem = m
	if r.prompt != nil {
		r.prompt.SetMemory(m)
	}
}

// SetSpecService injects spec-driven content (spec.md/tasks.md/checklist.md/CLAUDE.md)
// into the system prompt for each turn.
func (r *Runner) SetSpecService(s SpecProvider) {
	if r.prompt != nil {
		r.prompt.SetSpecService(s)
	}
}
func (r *Runner) SetCompressorLLM(summarizer *contextx.Summarizer) {
	if r.compressor != nil && summarizer != nil {
		r.compressor.SetSummarizer(summarizer)
	}
}

// SetIntentRouter 注入意图路由器
func (r *Runner) SetIntentRouter(router intent.Router) {
	r.intentRouter = router
}

// SetGraphStore overrides the Eino graph checkpoint store (optional).
func (r *Runner) SetGraphStore(store compose.CheckPointStore) {
	if r == nil {
		return
	}
	r.graphStore = store
	r.cfg.GraphResume = store != nil
}

func (r *Runner) Permission() *security.Guard { return r.perm }

func (r *Runner) setGraphInterrupt(sessionID, interruptID string) {
	if r == nil || sessionID == "" || interruptID == "" {
		return
	}
	r.graphMu.Lock()
	r.graphIntr[sessionID] = interruptID
	r.graphMu.Unlock()
}

func (r *Runner) takeGraphInterrupt(sessionID string) string {
	if r == nil {
		return ""
	}
	r.graphMu.Lock()
	defer r.graphMu.Unlock()
	id := r.graphIntr[sessionID]
	delete(r.graphIntr, sessionID)
	return id
}

func (r *Runner) peekGraphInterrupt(sessionID string) string {
	if r == nil {
		return ""
	}
	r.graphMu.Lock()
	defer r.graphMu.Unlock()
	return r.graphIntr[sessionID]
}

func (r *Runner) crossCut(userID string) *CrossCut {
	return &CrossCut{Hooks: r.hooks, Audit: r.audit, Cache: r.cache, UserID: userID}
}

func (r *Runner) Run(ctx context.Context, session *sessmodel.Session, userInput string, eventCh chan<- *engine.Event, opts engine.RunOptions) (*engine.Result, error) {
	ctx, span := observability.StartSpan(ctx, "eino.agent.run")
	defer span.End()

	publish := func(ev *engine.Event) {
		if eventCh == nil || ev == nil {
			return
		}
		select {
		case eventCh <- ev:
		case <-ctx.Done():
		case <-time.After(EventPublishTimeout):
		}
	}
	stats := &runStats{}
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return nil, fmt.Errorf("empty input")
	}

	if r.hooks != nil {
		r.hooks.Emit(ctx, hook.Event{Point: hook.SessionStart, SessionID: session.ID})
		defer r.hooks.Emit(ctx, hook.Event{Point: hook.SessionEnd, SessionID: session.ID})
	}

	publish(&engine.Event{Type: engine.EventThought, Content: "Eino ReAct + security cross-cuts", Timestamp: nowMs()})

	// --- HITL resume (prefer in-graph when CheckPointStore + interrupt id available) ---
	if r.perm != nil && isContinue(userInput) {
		if ready := r.perm.TakeReadyResume(session.ID); ready != nil {
			if r.graphStore != nil {
				if iid := r.peekGraphInterrupt(session.ID); iid != "" {
					publish(&engine.Event{
						Type: engine.EventResume, SubType: ready.Tool,
						Content: "graph resume " + ready.Tool + " interrupt=" + iid, Timestamp: nowMs(),
					})
					if res, ok := r.tryGraphResume(ctx, session, ready, iid, publish, stats, opts); ok {
						return res, nil
					}
					publish(&engine.Event{
						Type: engine.EventThought, Content: "graph resume fallback → app-level HITL", Timestamp: nowMs(),
					})
				}
			}
			// App-level: execute approved tool via GuardedTool cross-cuts, then continue ReAct.
			publish(&engine.Event{Type: engine.EventResume, SubType: ready.Tool, Content: "app resume " + ready.Tool, Timestamp: nowMs()})
			inner := r.tools.Get(ready.Tool)
			if inner == nil {
				msg := "resume failed: unknown tool " + ready.Tool
				publish(&engine.Event{Type: engine.EventError, SubType: "resume", Content: msg, Completed: true, Timestamp: nowMs()})
				return &engine.Result{SessionID: session.ID, Response: msg, ErrorClass: "tool"}, nil
			}
			gt := &GuardedTool{Inner: inner, Guard: r.perm, UseInterrupt: false, Cross: r.crossCut(session.UserID)}
			tctx := WithRunContext(ctx, RunContext{
				SessionID: session.ID, UserID: session.UserID, AutoApprove: true,
				Publish: publish, Cross: r.crossCut(session.UserID),
			})
			argsJSON, _ := jsonMarshal(ready.Args)
			resText, runErr := gt.InvokableRun(tctx, argsJSON)
			if runErr != nil {
				resText = "resume error: " + runErr.Error()
			}
			stats.toolCalls.Add(1)
			publish(&engine.Event{Type: engine.EventToolCall, SubType: ready.Tool, Content: ready.Tool, Data: ready.Args, Timestamp: nowMs()})
			publish(&engine.Event{Type: engine.EventObservation, SubType: ready.Tool, Content: truncate(resText, EventObservationMaxChars), Timestamp: nowMs()})
			publish(&engine.Event{Type: engine.EventToolResult, SubType: ready.Tool, Content: truncate(resText, EventResultMaxChars), Timestamp: nowMs()})
			if r.messages != nil {
				if err := r.messages.Save(ctx, &sessmodel.Message{
					ID: idMsg(), SessionID: session.ID, Role: "assistant",
					Content:   fmt.Sprintf("Thought: resume approved tool\nAction: %s", ready.Tool),
					CreatedAt: time.Now(), Priority: 3,
				}); err != nil {
					observability.LogError("save resume assistant message", err)
				}
				if err := r.messages.Save(ctx, &sessmodel.Message{
					ID: idMsg(), SessionID: session.ID, Role: "tool", Content: resText,
					ToolName: ready.Tool, ToolCallID: "resume-" + ready.PermID, CreatedAt: time.Now(),
					Priority: 2,
				}); err != nil {
					observability.LogError("save resume tool message", err)
				}
			}
			userInput = "Tool " + ready.Tool + " executed after human approval. Observation:\n" + truncate(resText, ResumeObservationMaxChars) +
				"\nContinue the task with Thought then Action or Final Answer."
		}
	}

	// skill match
	var activeSkill *skill.Skill
	if r.skills != nil && !isContinue(userInput) {
		activeSkill = r.skills.Match(userInput)
		if activeSkill == nil {
			activeSkill = r.skills.MatchSemantic(ctx, userInput)
		}
		if activeSkill != nil {
			publish(&engine.Event{Type: engine.EventSkill, SubType: activeSkill.ID, Content: "skill: " + activeSkill.Name, Timestamp: nowMs()})
		}
	}

	if r.messages != nil {
		um := sessmodel.NewMessage(idMsg(), session.ID, "user", userInput)
		um.Priority = 5
		um.TokenCount = common.EstimateTokens(userInput)
		if err := r.messages.Save(ctx, um); err != nil {
			observability.LogError("save user message", err)
		}
	}

	// 意图路由（带跨轮指代消解：从近期消息提取最近实体）
	var ir intent.Result
	if r.intentRouter != nil {
		var ec *intent.EntityContext
		if r.messages != nil {
			if recent, err := r.messages.ListBySession(ctx, session.ID, 12); err == nil {
				contents := make([]string, 0, len(recent))
				for _, m := range recent {
					contents = append(contents, m.Content)
				}
				ec = intent.ExtractEntities(contents)
			}
		}
		ir = r.intentRouter.ClassifyWithContext(userInput, ec)
	} else {
		ir = intent.Result{Intent: intent.IntentNormal, CleanInput: userInput}
	}
	publish(&engine.Event{Type: engine.EventThought, SubType: "intent", Content: "intent: " + ir.Intent.String() + " (source: " + ir.Source + ")", Timestamp: nowMs()})

	// M3/3.1: per-intent model routing (before any model init).
	r.applyRoute(ir.Intent.String())

	if r.Multi != nil && ir.Intent == intent.IntentDeep {
		return r.Multi.RunDeep(ctx, session, ir.CleanInput, publish, opts)
	}
	if r.Multi != nil && ir.Intent == intent.IntentTeam {
		return r.Multi.RunParallel(ctx, session, ir.CleanInput, publish, opts)
	}

	// --- history load + compress chain ---
	msgs, compressNote := r.loadAndCompress(ctx, session, userInput, opts.ForceCompact, publish)
	if compressNote != "" {
		publish(&engine.Event{Type: engine.EventCompress, Content: compressNote, Timestamp: nowMs()})
	}

	// token budget hard guard on input
	budget := r.cfg.TokenBudget
	if r.tokens != nil {
		budget = r.tokens.Budget
	}
	msgs = trimSchemaMessages(msgs, budget*BudgetInputRatioNum/BudgetInputRatioDen, DefaultTrimKeepTail)
	est := schemaToEstimateTokens(msgs)
	if est > budget {
		publish(&engine.Event{Type: engine.EventError, SubType: "budget", Content: fmt.Sprintf("token budget exceeded before LLM est=%d budget=%d", est, budget), Completed: true, Timestamp: nowMs()})
		return &engine.Result{SessionID: session.ID, Response: "stopped: token budget exceeded before model call", ErrorClass: "budget"}, nil
	}

	cm, err := r.newChatModel(ctx)
	if err != nil {
		publish(&engine.Event{Type: engine.EventError, Content: err.Error(), Completed: true, Timestamp: nowMs()})
		return &engine.Result{SessionID: session.ID, Response: "LLM init error: " + err.Error(), ErrorClass: "llm"}, nil
	}

	cross := r.crossCut(session.UserID)
	einoTools := WrapRegistryCross(r.tools, r.perm, cross)
	// skill tool filter
	if activeSkill != nil && len(activeSkill.Tools) > 0 {
		einoTools = filterEinoToolsByAllow(einoTools, activeSkill.Tools)
	}

	dynMod := r.prompt.MessageModifier(ctx, session.UserID, session.ProjectID, userInput, activeSkill, budget)
	// MessageRewriter: mid-loop state trim under budget
	rewriter := func(ctx context.Context, stateMsgs []*schema.Message) []*schema.Message {
		return trimSchemaMessages(stateMsgs, budget*BudgetInputRatioNum/BudgetInputRatioDen, DefaultRewriterKeepTail)
	}

	handle, err := buildReactAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: einoTools},
		MaxStep:          r.cfg.MaxSteps,
		MessageModifier:  dynMod,
		MessageRewriter:  rewriter,
		GraphName:        DefaultGraphName,
	}, r.graphStore)
	if err != nil {
		return &engine.Result{SessionID: session.ID, Response: "eino agent: " + err.Error(), ErrorClass: "eino"}, nil
	}

	tctx := WithRunContext(ctx, RunContext{
		SessionID: session.ID, UserID: session.UserID, AutoApprove: opts.AutoApprove,
		Publish: publish, Cross: cross,
	})

	cpID := DefaultGraphCheckPointID(session.ID)
	graphOn := r.graphStore != nil
	publish(&engine.Event{
		Type: engine.EventThought,
		Content: fmt.Sprintf("eino tools=%d maxStep=%d tokens~%d budget=%d stream=%v graphResume=%v",
			len(einoTools), r.cfg.MaxSteps, est, budget, r.cfg.UseStream, graphOn),
		Timestamp: nowMs(),
	})

	cbOpt := agentOptions(publish, stats)
	genOpts := []agent.AgentOption{cbOpt}
	if graphOn {
		// new user turn: force new run unless we are mid-graph-resume (handled separately)
		genOpts = append(genOpts, graphResumeOpts(cpID, true)...)
	}
	var final string
	var genErr error
	tLLM := time.Now()
	llmCtx, llmSpan := observability.StartSpan(tctx, "eino.llm.generate")
	if r.cfg.UseStream {
		final, genErr = r.runStream(llmCtx, handle, msgs, publish, genOpts...)
	} else {
		out, err := handle.Generate(llmCtx, msgs, genOpts...)
		genErr = err
		if out != nil {
			final = strings.TrimSpace(out.Content)
		}
	}
	llmSpan.End()
	observability.Current().ObserveLLM(time.Since(tLLM))
	observability.Current().AddChatTotal(1)

	if genErr != nil {
		if isInterruptErr(genErr) {
			if iid := ExtractFirstInterruptID(genErr); iid != "" {
				r.setGraphInterrupt(session.ID, iid)
				publish(&engine.Event{
					Type: engine.EventThought, SubType: "graph_interrupt",
					Content: "eino interrupt id=" + iid, Timestamp: nowMs(),
				})
			}
			pending := firstPending(r.perm, session.ID)
			msg := "CONFIRM required — human-in-the-loop (Eino interrupt)"
			if pending != nil {
				msg = fmt.Sprintf("CONFIRM required tool=%s id=%s reason=%s\nApprove then send 继续",
					pending.Tool, pending.ID, pending.Reason)
				publish(&engine.Event{
					Type: engine.EventPermission, SubType: "confirm", Content: msg,
					Data: pending, Completed: true, Timestamp: nowMs(),
				})
			}
			r.persistAssistant(ctx, session, msg)
			tok := stats.TokenUsed()
			if tok <= 0 {
				tok = common.EstimateTokens(userInput) + common.EstimateTokens(msg) + est
			}
			return &engine.Result{
				SessionID: session.ID, Response: msg, NeedPermission: true,
				Pending: pending, ErrorClass: "permission",
				ToolCalls: int(stats.toolCalls.Load()),
				TokenUsed: tok,
			}, nil
		}
		observability.Current().AddChatErrors(1)
		publish(&engine.Event{Type: engine.EventError, Content: genErr.Error(), Completed: true, Timestamp: nowMs()})
		return &engine.Result{SessionID: session.ID, Response: "eino generate: " + genErr.Error(), ErrorClass: "eino"}, nil
	}

	if final == "" {
		final = "done."
	}
	pending := firstPending(r.perm, session.ID)
	needPerm := pending != nil
	if needPerm && !strings.Contains(final, "CONFIRM") {
		final = fmt.Sprintf("CONFIRM required id=%s tool=%s\n%s", pending.ID, pending.Tool, final)
	}

	r.persistAssistant(ctx, session, final)
	// Prefer measured TokenUsage from model callbacks; fall back to heuristic estimate.
	tokenUsed := stats.TokenUsed()
	if tokenUsed <= 0 {
		tokenUsed = common.EstimateTokens(userInput) + common.EstimateTokens(final) + est
	}
	if r.sessions != nil {
		session.AddTokens(tokenUsed)
		if err := r.sessions.Save(ctx, session); err != nil {
			observability.LogError("save session tokens", err)
		}
	}
	observability.Current().AddTokens(int64(tokenUsed))

	tc := int(stats.toolCalls.Load())
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino", "toolCalls": tc, "toolErrors": stats.toolErrs.Load(),
		"tokenEst": tokenUsed, "promptTokens": stats.promptTokens.Load(), "completionTokens": stats.completionTokens.Load(),
	}, Timestamp: nowMs()})

	res := &engine.Result{
		SessionID: session.ID, Response: final, Steps: tc + 1, ToolCalls: tc,
		TokenUsed: tokenUsed, NeedPermission: needPerm, Pending: pending,
	}
	if needPerm {
		res.ErrorClass = "permission"
	}
	return res, nil
}

func (r *Runner) loadAndCompress(ctx context.Context, session *sessmodel.Session, userInput string, forceCompact bool, publish EventSink) ([]*schema.Message, string) {
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
	if needCompress && r.compressor != nil {
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
func (r *Runner) backgroundSummarize(ctx context.Context, session *sessmodel.Session, full []map[string]any, prior string) {
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

// tryGraphResume re-enters the Eino graph at the interrupted tool node after HITL approve.
// Returns (result, true) when graph path handled the turn; (nil, false) to fall back.
func (r *Runner) tryGraphResume(
	ctx context.Context,
	session *sessmodel.Session,
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
	})
	cpID := DefaultGraphCheckPointID(session.ID)
	cbOpt := agentOptions(publish, stats)
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

func (r *Runner) runStream(ctx context.Context, ag *agentHandle, msgs []*schema.Message, publish EventSink, opts ...agent.AgentOption) (string, error) {
	sr, err := ag.Stream(ctx, msgs, opts...)
	if err != nil {
		return "", err
	}
	defer sr.Close()
	var b strings.Builder
	// Accumulate partial tool-call argument streams (index → name/args).
	type tcAcc struct {
		name string
		args strings.Builder
		id   string
	}
	byIdx := map[int]*tcAcc{}
	emittedName := map[string]bool{}

	for {
		msg, err := sr.Recv()
		if err != nil {
			// Preserve interrupt / real failures; only EOF is normal stream end.
			if errors.Is(err, io.EOF) {
				break
			}
			// Some providers close with generic error string; treat pure EOF-like ends only.
			if err.Error() == "EOF" {
				break
			}
			return strings.TrimSpace(b.String()), err
		}
		if msg == nil {
			continue
		}
		if msg.Content != "" {
			b.WriteString(msg.Content)
			publish(&engine.Event{Type: engine.EventTextDelta, Content: msg.Content, Timestamp: nowMs()})
		}
		// Streamed tool_call deltas (name may arrive before/without full args).
		for _, tc := range msg.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			acc, ok := byIdx[idx]
			if !ok {
				acc = &tcAcc{}
				byIdx[idx] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" && acc.name == "" {
				acc.name = tc.Function.Name
				if !emittedName[acc.name] {
					emittedName[acc.name] = true
					publish(&engine.Event{
						Type: engine.EventToolCall, SubType: acc.name,
						Content:   acc.name,
						Data:      map[string]any{"id": acc.id, "stream": true},
						Timestamp: nowMs(),
					})
				}
			}
			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	// Publish finalized tool-call args when stream ends with complete JSON fragments.
	for idx, acc := range byIdx {
		if acc == nil || acc.name == "" {
			continue
		}
		raw := acc.args.String()
		if raw == "" {
			continue
		}
		args := map[string]any{}
		_ = json.Unmarshal([]byte(raw), &args)
		publish(&engine.Event{
			Type: engine.EventToolCall, SubType: acc.name,
			Content:   "args_complete",
			Data:      map[string]any{"index": idx, "id": acc.id, "args": args, "args_raw": truncate(raw, ArgsRawMaxChars)},
			Timestamp: nowMs(),
		})
	}
	return strings.TrimSpace(b.String()), nil
}

func (r *Runner) persistAssistant(ctx context.Context, session *sessmodel.Session, text string) {
	if r.messages == nil {
		return
	}
	am := sessmodel.NewMessage(idMsg(), session.ID, "assistant", text)
	am.Priority = 3
	am.TokenCount = common.EstimateTokens(text)
	if err := r.messages.Save(ctx, am); err != nil {
		observability.LogError("save assistant message", err)
	}
}

// applyRoute selects the model endpoint for the given intent and updates the
// active config so subsequent newChatModel calls use it. No-op when no Router
// is configured (single-model behavior preserved).
func (r *Runner) applyRoute(intent string) {
	if r.cfg.Router == nil {
		return
	}
	route := r.cfg.Router.Select(intent)
	if route.Model != "" {
		r.cfg.Model = route.Model
	}
	if route.APIBase != "" {
		r.cfg.APIBase = route.APIBase
	}
	if route.APIKey != "" {
		r.cfg.APIKey = route.APIKey
	}
}

func (r *Runner) newChatModel(ctx context.Context) (*openai.ChatModel, error) {
	cfg := &openai.ChatModelConfig{
		APIKey: r.cfg.APIKey,
		Model:  r.cfg.Model,
	}
	if r.cfg.APIBase != "" {
		cfg.BaseURL = r.cfg.APIBase
	}
	if r.cfg.ByAzure {
		cfg.ByAzure = true
		cfg.APIVersion = r.cfg.APIVersion
	}
	temp := float32(0.2)
	cfg.Temperature = &temp
	return openai.NewChatModel(ctx, cfg)
}

func filterEinoToolsByAllow(tools []einotool.BaseTool, allow []string) []einotool.BaseTool {
	if len(allow) == 0 {
		return tools
	}
	ok := map[string]bool{}
	for _, a := range allow {
		ok[a] = true
		if a == "*" {
			return tools
		}
	}
	var out []einotool.BaseTool
	for _, t := range tools {
		if t == nil {
			continue
		}
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		if ok[info.Name] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return tools
	}
	return out
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func defaultPersona() string {
	return NewPromptContext().Header() + "\n\n" +
		WorkPolicySection() + "\n\n" +
		`<tool_calling>
- Use specialized tools instead of bash when possible. Prefer dedicated file tools for read/edit over shell commands.
- Reserve shell commands exclusively for actual system operations.
- NEVER use bash echo to communicate with the user. Output all communication in your response text.
</tool_calling>` + "\n\n" +
		CommunicationSection() + "\n\n" +
		FormattingSection()
}

func isContinue(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "继续" || s == "continue" || s == "ok" || s == "y" || s == "yes" || s == "继续执行"
}

func looksMulti(s string) bool {
	ls := strings.ToLower(s)
	return strings.HasPrefix(ls, "/team") || strings.HasPrefix(ls, "/parallel") ||
		strings.Contains(ls, "parallel explore") || strings.Contains(ls, "team mode")
}

func looksDeep(s string) bool {
	return deepagent.LooksDeep(s)
}

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

func nowMs() int64 { return time.Now().UnixMilli() }

func idMsg() string {
	return fmt.Sprintf("msg-%d", time.Now().UnixNano())
}

var _ engine.Runner = (*Runner)(nil)
