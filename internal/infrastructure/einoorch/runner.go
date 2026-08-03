package einoorch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
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
}

// Runner uses CloudWeGo Eino ReAct; security/business cross-cuts stay in domain tools.
type Runner struct {
	cfg        Config
	tools      *tool.MapRegistry
	perm       *security.Guard
	sessions   sessrepo.ISessionRepository
	messages   sessrepo.IMessageRepository
	summaries  sessrepo.ISummaryRepository
	hooks      *hook.Bus
	audit      audit.Repository
	cache      *tool.ResultCache
	compressor *contextx.Compressor
	prompt     *PromptBuilder
	skills     *skill.Service
	mem        *memory.Service
	persona    string
	Multi      *MultiAgent
	tokens     *engine.TokenManager
}

func NewRunner(
	cfg Config,
	tools *tool.MapRegistry,
	perm *security.Guard,
	sessions sessrepo.ISessionRepository,
	messages sessrepo.IMessageRepository,
) *Runner {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 20
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = 32000
	}
	comp := contextx.NewCompressor(cfg.TokenBudget / 2)
	r := &Runner{
		cfg: cfg, tools: tools, perm: perm,
		sessions: sessions, messages: messages,
		persona:    defaultPersona(),
		cache:      tool.NewResultCache(30*time.Second, 128),
		compressor: comp,
		prompt:     NewPromptBuilder(defaultPersona(), tools),
		tokens:     engine.NewTokenManager(cfg.TokenBudget),
	}
	r.Multi = NewMultiAgent(r)
	return r
}

func (r *Runner) SetHooks(h *hook.Bus)                        { r.hooks = h }
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
func (r *Runner) SetCompressorLLM(summarizer *contextx.Summarizer) {
	if r.compressor != nil && summarizer != nil {
		r.compressor.SetSummarizer(summarizer)
	}
}

func (r *Runner) Permission() *security.Guard { return r.perm }

func (r *Runner) crossCut(userID string) *CrossCut {
	return &CrossCut{Hooks: r.hooks, Audit: r.audit, Cache: r.cache, UserID: userID}
}

func (r *Runner) Run(ctx context.Context, session *sessmodel.Session, userInput string, eventCh chan<- *engine.Event, opts engine.RunOptions) (*engine.Result, error) {
	publish := func(ev *engine.Event) {
		if eventCh == nil || ev == nil {
			return
		}
		select {
		case eventCh <- ev:
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
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

	// --- resume approved tool ---
	if r.perm != nil && isContinue(userInput) {
		if ready := r.perm.TakeReadyResume(session.ID); ready != nil {
			publish(&engine.Event{Type: engine.EventResume, SubType: ready.Tool, Content: "resume " + ready.Tool, Timestamp: nowMs()})
			// execute through GuardedTool path for audit/hooks
			gt := &GuardedTool{Inner: r.tools.Get(ready.Tool), Guard: r.perm, UseInterrupt: false, Cross: r.crossCut(session.UserID)}
			tctx := WithRunContext(ctx, RunContext{
				SessionID: session.ID, UserID: session.UserID, AutoApprove: true,
				Publish: publish, Cross: r.crossCut(session.UserID),
			})
			argsJSON, _ := jsonMarshal(ready.Args)
			resText, _ := gt.InvokableRun(tctx, argsJSON)
			stats.toolCalls.Add(1)
			publish(&engine.Event{Type: engine.EventToolCall, SubType: ready.Tool, Content: ready.Tool, Data: ready.Args, Timestamp: nowMs()})
			publish(&engine.Event{Type: engine.EventObservation, SubType: ready.Tool, Content: truncate(resText, 800), Timestamp: nowMs()})
			if r.messages != nil {
				_ = r.messages.Save(ctx, &sessmodel.Message{
					ID: idMsg(), SessionID: session.ID, Role: "tool", Content: resText,
					ToolName: ready.Tool, ToolCallID: "resume", CreatedAt: time.Now(),
				})
			}
			userInput = "Tool " + ready.Tool + " executed after approval. Observation:\n" + truncate(resText, 1500) +
				"\nContinue the task."
		}
	}

	// skill match
	var activeSkill *skill.Skill
	if r.skills != nil && !isContinue(userInput) {
		activeSkill = r.skills.Match(userInput)
		if activeSkill != nil {
			publish(&engine.Event{Type: engine.EventSkill, SubType: activeSkill.ID, Content: "skill: " + activeSkill.Name, Timestamp: nowMs()})
		}
	}

	if r.messages != nil {
		um := sessmodel.NewMessage(idMsg(), session.ID, "user", userInput)
		um.Priority = 5
		um.TokenCount = common.EstimateTokens(userInput)
		_ = r.messages.Save(ctx, um)
	}

	if r.Multi != nil && looksDeep(userInput) {
		return r.Multi.RunDeep(ctx, session, userInput, publish, opts)
	}
	if r.Multi != nil && looksMulti(userInput) {
		return r.Multi.RunParallel(ctx, session, userInput, publish, opts)
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
	msgs = trimSchemaMessages(msgs, budget*3/4, 16)
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
		return trimSchemaMessages(stateMsgs, budget*3/4, 14)
	}

	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: einoTools},
		MaxStep:          r.cfg.MaxSteps,
		MessageModifier:  dynMod,
		MessageRewriter:  rewriter,
	})
	if err != nil {
		return &engine.Result{SessionID: session.ID, Response: "eino agent: " + err.Error(), ErrorClass: "eino"}, nil
	}

	tctx := WithRunContext(ctx, RunContext{
		SessionID: session.ID, UserID: session.UserID, AutoApprove: opts.AutoApprove,
		Publish: publish, Cross: cross,
	})

	publish(&engine.Event{
		Type: engine.EventThought,
		Content: fmt.Sprintf("eino tools=%d maxStep=%d tokens~%d budget=%d stream=%v",
			len(einoTools), r.cfg.MaxSteps, est, budget, r.cfg.UseStream),
		Timestamp: nowMs(),
	})

	cbOpt := agentOptions(publish, stats)
	var final string
	var genErr error
	tLLM := time.Now()
	if r.cfg.UseStream {
		final, genErr = r.runStream(tctx, agentInst, msgs, publish, cbOpt)
	} else {
		out, err := agentInst.Generate(tctx, msgs, cbOpt)
		genErr = err
		if out != nil {
			final = strings.TrimSpace(out.Content)
		}
	}
	observability.Global.ObserveLLM(time.Since(tLLM))
	observability.Global.ChatTotal.Add(1)

	if genErr != nil {
		if isInterruptErr(genErr) {
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
			return &engine.Result{
				SessionID: session.ID, Response: msg, NeedPermission: true,
				Pending: pending, ErrorClass: "permission",
				ToolCalls: int(stats.toolCalls.Load()),
				TokenUsed: common.EstimateTokens(userInput) + common.EstimateTokens(msg),
			}, nil
		}
		observability.Global.ChatErrors.Add(1)
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
	tokenUsed := common.EstimateTokens(userInput) + common.EstimateTokens(final) + est
	if r.sessions != nil {
		session.AddTokens(tokenUsed)
		_ = r.sessions.Save(ctx, session)
	}
	observability.Global.TokensTotal.Add(int64(tokenUsed))

	tc := int(stats.toolCalls.Load())
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino", "toolCalls": tc, "toolErrors": stats.toolErrs.Load(), "tokenEst": tokenUsed,
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
	recent, _ := r.messages.ListAsMaps(ctx, session.ID, 24)
	full := recent
	needCompress := forceCompact || (r.compressor != nil && r.compressor.Needs(recent))
	if needCompress || len(recent) >= 24 {
		if more, err := r.messages.ListAsMaps(ctx, session.ID, 120); err == nil {
			full = more
		}
	}
	note := ""
	if needCompress && r.compressor != nil {
		if r.hooks != nil {
			r.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
		}
		prior := ""
		if r.summaries != nil {
			prior, _ = r.summaries.Get(ctx, session.ID)
		}
		useSum := forceCompact || len(full) > 16
		cr := r.compressor.CompressLevels(ctx, full, prior, useSum)
		full = cr.History
		if cr.Summary != "" && r.summaries != nil {
			_ = r.summaries.Save(ctx, session.ID, cr.Summary, common.EstimateTokens(cr.Summary))
		}
		observability.Global.CompressTotal.Add(1)
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

func (r *Runner) runStream(ctx context.Context, ag *react.Agent, msgs []*schema.Message, publish EventSink, opts ...agent.AgentOption) (string, error) {
	sr, err := ag.Stream(ctx, msgs, opts...)
	if err != nil {
		return "", err
	}
	defer sr.Close()
	var b strings.Builder
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		if msg == nil {
			continue
		}
		if msg.Content != "" {
			b.WriteString(msg.Content)
			publish(&engine.Event{Type: engine.EventTextDelta, Content: msg.Content, Timestamp: nowMs()})
		}
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
	_ = r.messages.Save(ctx, am)
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
	return `You are Code-Agent on Eino ReAct orchestration.
Sandboxed workspace. Use tools for file/shell. Prefer edit_file.
If a tool returns DENIED or CONFIRM, explain what the user must approve.
For multi-step independent research, you may use the delegate tool when available.
Prefer code_search for symbol/file discovery before blind glob.
For deep multi-step implementation use prefix /deep ; for parallel roles use /team .
Be concise.`
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

func isInterruptErr(err error) bool {
	if err == nil {
		return false
	}
	var target interface{ GetInterruptContexts() any }
	if errors.As(err, &target) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "interrupt") || strings.Contains(msg, "Interrupt")
}

func nowMs() int64 { return time.Now().UnixMilli() }

func idMsg() string {
	return fmt.Sprintf("msg-%d", time.Now().UnixNano())
}

var _ engine.Runner = (*Runner)(nil)
