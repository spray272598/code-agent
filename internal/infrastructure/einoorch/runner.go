package einoorch

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/eval"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/intent"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/model"
	"github.com/spray272598/code-agent/internal/domain/orchestration"
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
	// TeamConfigFile is the YAML path for team role configuration.
	// Empty → uses default "teams/default.yaml" (with fallback to hardcoded defaults).
	TeamConfigFile string
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

	// skillToolNames tracks skill-derived tool names for cleanup on re-assignment.
	skillToolNames map[string]bool

	// optional in-graph interrupt resume
	graphStore compose.CheckPointStore
	graphMu    sync.Mutex
	// sessionID → last interrupt context id (process-local; durable via graph store)
	graphIntr map[string]string

	// evalCollector gathers per-session evaluation metrics (nil = disabled).
	evalCollector *eval.Collector
	// sloMonitor tracks SLO compliance (latency, error rate, availability).
	sloMonitor *observability.SLOMonitor
	// ctxIntegrator coordinates budget/compression/memory enrichment (nil = uses compressor directly).
	ctxIntegrator *contextx.ContextIntegrator
	// budgetMgr enables dynamic token budget allocation (nil = uses static budget).
	budgetMgr *contextx.BudgetManager
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
	r.Multi = NewMultiAgent(r, cfg.TeamConfigFile)
	return r
}

func (r *Runner) SetHooks(h *hook.Bus)                         { r.hooks = h }
func (r *Runner) SetAudit(a audit.Repository)                  { r.audit = a }
func (r *Runner) SetSummaryRepo(s sessrepo.ISummaryRepository) { r.summaries = s }
func (r *Runner) SetSkills(s *skill.Service) {
	// Cleanup previously registered skill tools.
	for name := range r.skillToolNames {
		r.tools.Unregister(name)
	}
	r.skillToolNames = map[string]bool{}

	r.skills = s
	if s != nil {
		if tools := s.BuildSkillTools(); len(tools) > 0 {
			for _, st := range tools {
				r.tools.Register(st)
				r.skillToolNames[st.Name()] = true
			}
			log.Printf("[runner] registered %d skill tools: %v\n", len(tools), r.skillToolNames)
		}
	}
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

// SetEvalCollector injects the evaluation metrics collector (nil = disabled).
func (r *Runner) SetEvalCollector(c *eval.Collector) {
	r.evalCollector = c
}

// SetSLOMonitor injects the SLO compliance monitor (nil = disabled).
func (r *Runner) SetSLOMonitor(m *observability.SLOMonitor) {
	r.sloMonitor = m
}

// SetContextIntegrator injects the context coordination engine (nil = disabled).
func (r *Runner) SetContextIntegrator(ci *contextx.ContextIntegrator) {
	r.ctxIntegrator = ci
}

// SetBudgetManager injects the dynamic token budget manager (nil = disabled).
func (r *Runner) SetBudgetManager(bm *contextx.BudgetManager) {
	r.budgetMgr = bm
}

// SetJournalConfig configures the journal persistence backend for DeepAgent runs.
// Supports file (default), MySQL, Redis, and in-memory storage.
func (r *Runner) SetJournalConfig(cfg orchestration.JournalStorageConfig) {
	if r.Multi != nil {
		r.Multi.SetJournalConfig(cfg)
	}
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

	// --- Evaluation: begin session metrics collection ---
	if r.evalCollector != nil {
		_ = r.evalCollector.BeginSession(session.ID, session.UserID)
	}
	// --- SLO monitoring: record agent latency start ---
	agentStart := time.Now()

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

	// --- Topology selection: use Orchestrator Router as the primary decision maker ---
	// The intent classifier handles continue detection and entity extraction;
	// Router handles all topology decisions (single/teams/deep).
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

	// Extract explicit topology hint from intent (for backward-compatible prefixes).
	explicitMode := orchestration.ModeSingleAgent
	if ir.Intent == intent.IntentDeep {
		explicitMode = orchestration.ModeDeepAgent
	} else if ir.Intent == intent.IntentTeam {
		explicitMode = orchestration.ModeTeams
	}

	// Router decides the topology (explicit hint takes priority, then auto-detection).
	var topologyMode orchestration.OrchestratorMode
	var topologyNote string
	if r.Multi != nil && r.Multi.Router() != nil {
		topologyMode = r.Multi.Router().Decide(ir.CleanInput, explicitMode)
		topologyNote = r.Multi.Router().Describe(ir.CleanInput, topologyMode)
	} else {
		topologyMode = explicitMode
		topologyNote = "single-agent (no router)"
	}

	publish(&engine.Event{
		Type: engine.EventThought, SubType: "router",
		Content: fmt.Sprintf("topology: %s | %s | intent=%s source=%s",
			topologyMode.String(), topologyNote, ir.Intent.String(), ir.Source),
		Timestamp: nowMs(),
	})

	// --- Evaluation: record topology selection ---
	if r.evalCollector != nil {
		topology := topologyMode.String()
		r.evalCollector.SetTopology(session.ID, topology)
	}

	// M3/3.1: per-intent model routing (before any model init).
	r.applyRoute(ir.Intent.String())

	// --- Route to the appropriate agent topology ---
	if r.Multi != nil && topologyMode == orchestration.ModeDeepAgent {
		publish(&engine.Event{
			Type: engine.EventThought, SubType: "router",
			Content:   "router: selected DeepAgent topology for: " + truncate(ir.CleanInput, 60),
			Timestamp: nowMs(),
		})
		return r.Multi.RunDeep(ctx, session, ir.CleanInput, publish, opts)
	}
	if r.Multi != nil && topologyMode == orchestration.ModeTeams {
		publish(&engine.Event{
			Type: engine.EventThought, SubType: "router",
			Content:   "router: selected Teams topology for: " + truncate(ir.CleanInput, 60),
			Timestamp: nowMs(),
		})
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
		EvalCollector: r.evalCollector,
	})

	cpID := DefaultGraphCheckPointID(session.ID)
	graphOn := r.graphStore != nil
	publish(&engine.Event{
		Type: engine.EventThought,
		Content: fmt.Sprintf("eino tools=%d maxStep=%d tokens~%d budget=%d stream=%v graphResume=%v",
			len(einoTools), r.cfg.MaxSteps, est, budget, r.cfg.UseStream, graphOn),
		Timestamp: nowMs(),
	})

	cbOpt := agentOptionsWithEval(publish, stats, session.ID, r.evalCollector)
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
	llmLatency := time.Since(tLLM)
	observability.Current().ObserveLLM(llmLatency)
	observability.Current().AddChatTotal(1)

	// --- Evaluation: record LLM call metrics ---
	if r.evalCollector != nil {
		r.evalCollector.AddSample(session.ID, eval.Sample{
			Dimension: eval.DimEfficiency, Name: "llm_calls",
			Type: eval.SampleCounter, Value: 1,
		})
		if llmLatency > 0 {
			r.evalCollector.AddSample(session.ID, eval.Sample{
				Dimension: eval.DimEfficiency, Name: "llm_latency_ms",
				Type: eval.SampleHistogram, Value: float64(llmLatency.Milliseconds()),
			})
		}
	}
	// --- SLO: record LLM latency ---
	if r.sloMonitor != nil {
		r.sloMonitor.RecordLatency("llm_call_latency", llmLatency, genErr)
	}

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
				// --- Evaluation: record permission deny ---
				if r.evalCollector != nil {
					r.evalCollector.AddSample(session.ID, eval.Sample{
						Dimension: eval.DimSafety, Name: "permission_deny",
						Type: eval.SampleCounter, Value: 1,
					})
					r.evalCollector.EndSession(session.ID, false, "permission")
				}
				if r.sloMonitor != nil {
					r.sloMonitor.RecordError("agent_latency")
				}
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
		// --- Evaluation: record LLM error ---
		if r.evalCollector != nil {
			r.evalCollector.AddSample(session.ID, eval.Sample{
				Dimension: eval.DimEfficiency, Name: "llm_error",
				Type: eval.SampleCounter, Value: 1,
			})
			r.evalCollector.EndSession(session.ID, false, "llm")
		}
		if r.sloMonitor != nil {
			r.sloMonitor.RecordError("llm_call_latency")
		}
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

	// --- Evaluation: record token and step metrics ---
	if r.evalCollector != nil {
		r.evalCollector.AddSample(session.ID, eval.Sample{
			Dimension: eval.DimEfficiency, Name: "tokens_total",
			Type: eval.SampleCounter, Value: float64(tokenUsed),
		})
		promptTokens := int(stats.promptTokens.Load())
		completionTokens := int(stats.completionTokens.Load())
		if promptTokens > 0 {
			r.evalCollector.AddSample(session.ID, eval.Sample{
				Dimension: eval.DimEfficiency, Name: "tokens_input",
				Type: eval.SampleCounter, Value: float64(promptTokens),
			})
		}
		if completionTokens > 0 {
			r.evalCollector.AddSample(session.ID, eval.Sample{
				Dimension: eval.DimEfficiency, Name: "tokens_output",
				Type: eval.SampleCounter, Value: float64(completionTokens),
			})
		}
		tcVal := int(stats.toolCalls.Load())
		if tcVal > 0 {
			r.evalCollector.AddSample(session.ID, eval.Sample{
				Dimension: eval.DimEfficiency, Name: "tool_calls",
				Type: eval.SampleCounter, Value: float64(tcVal),
			})
		}
		// Record compression quality.
		if compressNote != "" {
			r.evalCollector.AddSample(session.ID, eval.Sample{
				Dimension: eval.DimContextQuality, Name: "compression_applied",
				Type: eval.SampleBool, Value: 1,
			})
		}
	}

	// --- SLO: record agent latency ---
	if r.sloMonitor != nil {
		r.sloMonitor.RecordLatency("agent_latency", time.Since(agentStart), nil)
	}

	// --- Evaluation: end session with final metrics ---
	if r.evalCollector != nil {
		completed := true
		errorClass := ""
		if needPerm {
			errorClass = "permission"
		}
		r.evalCollector.EndSession(session.ID, completed, errorClass)
	}

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

var _ engine.Runner = (*Runner)(nil)
