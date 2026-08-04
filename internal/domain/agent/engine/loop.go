package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/agent/plan"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/types/common"
)

// maxToolResultChars aliases the package constant for local call sites.
const maxToolResultChars = MaxToolResultChars

type Loop struct {
	llm          port.ILLMPort
	tools        *tool.MapRegistry
	sessions     sessrepo.ISessionRepository
	messages     sessrepo.IMessageRepository
	summaries    sessrepo.ISummaryRepository
	perm         *security.Guard
	compressor   *contextx.Compressor
	skills       *skill.Service
	hooks        *hook.Bus
	specSvc      SpecService
	memSvc       *memory.Service
	memCtx       *coding.MemoryContext
	audit        audit.Repository
	subRunner    *subagent.Runner
	blobs        blob.Store
	blobThresh   int
	toolCache    *tool.ResultCache
	tokens       *TokenManager
	histLoader   *HistoryLoader
	maxRounds    int
	tokenBudget  int
	systemPrompt string
	// system prompt cache (tools + skill id)
	sysCacheKey string
	sysCacheVal string
}

func NewLoop(
	llm port.ILLMPort,
	tools *tool.MapRegistry,
	sessions sessrepo.ISessionRepository,
	messages sessrepo.IMessageRepository,
	perm *security.Guard,
	maxRounds, tokenBudget int,
) *Loop {
	if maxRounds <= 0 {
		maxRounds = DefaultMaxRounds
	}
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	comp := contextx.NewCompressor(tokenBudget / 2)
	comp.SetSummarizer(contextx.NewSummarizer(llm))
	return &Loop{
		llm: llm, tools: tools, sessions: sessions, messages: messages,
		perm: perm, compressor: comp,
		maxRounds: maxRounds, tokenBudget: tokenBudget,
		systemPrompt: defaultSystem(),
		toolCache:    tool.NewResultCache(30*time.Second, 128),
		tokens:       NewTokenManager(tokenBudget),
		histLoader:   NewHistoryLoader(messages, comp),
	}
}

func (l *Loop) SetSkills(s *skill.Service)   { l.skills = s }
func (l *Loop) SetHooks(h *hook.Bus)         { l.hooks = h }
func (l *Loop) SetSpecService(s SpecService) { l.specSvc = s }
func (l *Loop) SetMemory(svc *memory.Service, mc *coding.MemoryContext) {
	l.memSvc = svc
	l.memCtx = mc
}
func (l *Loop) SetAudit(a audit.Repository)                  { l.audit = a }
func (l *Loop) SetSummaryRepo(s sessrepo.ISummaryRepository) { l.summaries = s }
func (l *Loop) SetSubRunner(r *subagent.Runner)              { l.subRunner = r }
func (l *Loop) SetBlobStore(s blob.Store, threshold int) {
	l.blobs = s
	if threshold <= 0 {
		threshold = blob.DefaultThreshold
	}
	l.blobThresh = threshold
}

func (l *Loop) maybeOffload(ctx context.Context, sessionID, toolName, resText string) string {
	if l.blobs == nil {
		return budget(resText)
	}
	or := blob.OffloadIfLarge(ctx, l.blobs, sessionID, toolName, resText, l.blobThresh)
	if or.Offloaded {
		telemetry.IncBlobOffload()
		telemetry.TraceEvent(map[string]any{
			"event": "blob_offload", "session": sessionID, "tool": toolName, "bytes": or.Bytes, "key": or.ObjectKey,
		})
		return or.Preview
	}
	return budget(resText)
}

func defaultSystem() string {
	return `You are Code-Agent, a coding agent like Claude Code.
You work inside a sandboxed project workspace.

## ReAct protocol (required every turn)
You MUST reason actively before acting. Each assistant turn uses this format:

Thought: <your analysis of the goal, what you know, what to do next>
Action: {"name":"tool_name","args":{...}}
  �?or multiple tools: Action: [{"name":"...","args":{...}}, ...]
    (read-only tools execute in parallel; write/bash serially)
  �?pure JSON tool call(s) without the Action: label is also accepted
Final Answer: <user-facing answer when no more tools are needed>

After tools run, you will receive Observation(...): results. Then emit a new Thought and either another Action or Final Answer.
Do NOT skip Thought. Reflection on failure is part of Thought, not a separate mode.
Respect token budget: be concise; prefer Final Answer when enough evidence is collected.

## Tools
Core: read_file, write_file, edit_file, bash, glob, grep, memory_save, memory_search, delegate.
- memory_save / memory_search for durable user/project facts
- delegate for SubAgents (roles: explore|verify|general)
- edit_file supports multi-line exact replace and regex (regex=true)
- glob supports ** via doublestar; grep supports context (context_before/after or -C)

Prefer edit_file over full write for existing files. Be concise.
Dangerous operations require user confirmation. All tools (including MCP server__tool) go through permission checks.
If a tool fails: Thought should diagnose root cause and pick a different path/tool.`
}

// SpecService is the minimal spec service interface used by the engine.
// Implemented by spec.Service in domain/spec.
type SpecService interface {
	PromptSection() string
	HasSpec() bool
	HasCLAUDE() bool
	Reload() error
	Progress() float64
	Summary() string
	// plan.SpecData compatibility
	HasContent() bool
	GetTitle() string
	GetGoal() string
	GetTasks() []plan.TaskData
	GetChecklist() []plan.ChecklistData
	GetConstraints() []string
	GetAcceptance() []string
}

type RunOptions struct {
	AutoApprove  bool
	ForceCompact bool
}

func (l *Loop) Run(ctx context.Context, session *sessmodel.Session, userInput string, eventCh chan<- *Event, opts RunOptions) (*Result, error) {
	ctx, span := telemetry.StartSpan(ctx, "agent.run", map[string]string{
		"session.id": session.ID,
		"user.id":    session.UserID,
	})
	defer span.End()

	var droppedEvents int
	publish := func(ev *Event) {
		if eventCh == nil || ev == nil {
			return
		}
		// Prefer non-blocking; on full buffer, wait briefly then drop with metric
		// (never silent-drop without counting). Critical events block longer.
		critical := ev.Type == EventError || ev.Type == EventAnswer || ev.Type == EventDone ||
			ev.Type == EventPermission || ev.Completed
		timeout := 50 * time.Millisecond
		if critical {
			timeout = 2 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case eventCh <- ev:
		case <-ctx.Done():
			droppedEvents++
		case <-timer.C:
			droppedEvents++
			telemetry.IncChatError() // reuse counter; SSE drop signal
			if critical {
				// last-ditch: try once more without timeout using default case
				select {
				case eventCh <- ev:
					droppedEvents--
				default:
					telemetry.Warnf("sse drop critical event type=%s session=%s", ev.Type, session.ID)
				}
			}
		}
	}
	auditLog := func(action, toolName, detail, decision string, latencyMs int64) {
		if l.audit == nil {
			return
		}
		if err := l.audit.Append(ctx, audit.Entry{
			UserID: session.UserID, SessionID: session.ID,
			Action: action, Tool: toolName, Detail: detail, Decision: decision, LatencyMs: latencyMs,
		}); err != nil {
			telemetry.Warnf("audit append: %v", err)
		}
	}
	saveMsg := func(m *sessmodel.Message) {
		if m == nil {
			return
		}
		if err := l.messages.Save(ctx, m); err != nil {
			telemetry.Warnf("message save session=%s role=%s: %v", session.ID, m.Role, err)
		}
	}
	saveSess := func() error {
		if err := l.sessions.Save(ctx, session); err != nil {
			telemetry.Errorf("session save %s: %v", session.ID, err)
			return err
		}
		return nil
	}

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return nil, fmt.Errorf("empty input")
	}
	continuing := isContinue(userInput)

	if l.hooks != nil {
		l.hooks.Emit(ctx, hook.Event{Point: hook.SessionStart, SessionID: session.ID})
	}
	// wire subagent progress �?SSE
	if l.subRunner != nil {
		l.subRunner.OnProgress = func(p subagent.Progress) {
			publish(&Event{
				Type: EventSubAgent, SubType: p.Status, Content: p.Message,
				Data: p, Timestamp: now(),
			})
		}
		defer func() { l.subRunner.OnProgress = nil }()
	}
	if l.memCtx != nil {
		l.memCtx.Bind(session.UserID, session.ProjectID)
	}
	if l.memSvc != nil && !continuing {
		l.memSvc.MaybeExtractFromUserCorrection(ctx, session.UserID, session.ProjectID, session.ID, userInput)
	}

	var activeSkill *skill.Skill
	if l.skills != nil && !continuing {
		activeSkill = l.skills.Match(userInput)
		if activeSkill != nil {
			publish(&Event{Type: EventSkill, SubType: activeSkill.ID, Content: "skill: " + activeSkill.Name, Data: activeSkill, Timestamp: now()})
		}
	}

	// plan: spec-driven (if available) or rule-driven
	var taskPlan *plan.Plan
	if !continuing {
		if l.specSvc != nil && l.specSvc.HasSpec() {
			taskPlan = plan.BuildFromSpec(l.specSvc)
			if taskPlan == nil {
				taskPlan = plan.BuildRulePlan(userInput)
			}
		} else {
			taskPlan = plan.BuildRulePlan(userInput)
		}
		if taskPlan != nil {
			publish(&Event{Type: EventPlan, Content: taskPlan.Goal, Data: taskPlan, Timestamp: now()})
		}
	}

	um := sessmodel.NewMessage(id("msg"), session.ID, "user", userInput)
	um.Priority = messagePriority("user", userInput)
	um.TokenCount = common.EstimateTokens(userInput)
	saveMsg(um)
	session.AddTokens(um.TokenCount)

	// Lazy history: recent window first; full load only when compress pressure
	history, fullLoad, histErr := l.histLoader.Load(ctx, session.ID, opts.ForceCompact, session.MessageCount)
	if histErr != nil {
		telemetry.Warnf("history load: %v", histErr)
	}
	priorSummary := ""
	if l.summaries != nil {
		var sumErr error
		priorSummary, sumErr = l.summaries.Get(ctx, session.ID)
		if sumErr != nil {
			telemetry.Warnf("summary get: %v", sumErr)
		}
	}
	if opts.ForceCompact || l.compressor.Needs(history) {
		if l.hooks != nil {
			l.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
		}
		// ensure full history when compressing
		if !fullLoad {
			if full, err := l.messages.ListAsMaps(ctx, session.ID, DefaultHistoryLimit); err == nil && len(full) > len(history) {
				history = full
			}
		}
		useSum := opts.ForceCompact || len(history) > DefaultCompactThreshold || estimateMaps(history)*BudgetPressureRatio > l.tokenBudget
		cr := l.compressor.CompressLevels(ctx, history, priorSummary, useSum)
		history = cr.History
		if cr.Summary != "" && l.summaries != nil {
			if err := l.summaries.Save(ctx, session.ID, cr.Summary, common.EstimateTokens(cr.Summary)); err != nil {
				telemetry.Warnf("summary save: %v", err)
			}
		}
		telemetry.IncCompress()
		publish(&Event{Type: EventCompress, Content: fmt.Sprintf("compress %s saved~%d fullLoad=%v", cr.Level, cr.Saved, fullLoad), Data: map[string]any{"level": cr.Level, "summary": cr.Summary != "", "fullLoad": fullLoad}, Timestamp: now()})
		auditLog("compress", "", cr.Level, "ok", 0)
		telemetry.TraceEvent(map[string]any{"event": "compress", "session": session.ID, "level": cr.Level, "saved": cr.Saved})
	}

	// skill tools: merge depends allowlists
	var skillForTools *skill.Skill
	if activeSkill != nil {
		skillForTools = activeSkill
		if l.skills != nil {
			if merged := l.skills.MergedTools(activeSkill); len(merged) > 0 {
				cp := *activeSkill
				cp.Tools = merged
				skillForTools = &cp
			}
		}
	}
	skillID := ""
	if activeSkill != nil {
		skillID = activeSkill.ID
	}
	toolsKey := toolsFingerprint(l.tools)
	cacheKey := toolsKey + "|" + skillID
	var sys string
	if cacheKey == l.sysCacheKey && l.sysCacheVal != "" {
		sys = l.sysCacheVal
	} else {
		toolDesc := filterToolDescriptions(l.tools.Descriptions(), skillForTools)
		sys = l.systemPrompt + "\n\n## Available tools\n" + formatTools(toolDesc)
		if activeSkill != nil && l.skills != nil {
			sys += "\n" + l.skills.PromptSection(activeSkill)
			if skillForTools != nil && len(skillForTools.Tools) > 0 {
				sys += "\nYou may ONLY use these tools while this skill is active: " + strings.Join(skillForTools.Tools, ", ") + "\n"
			}
		}
		l.sysCacheKey = cacheKey
		l.sysCacheVal = sys
	}
	if l.memSvc != nil {
		memBlock := l.memSvc.FormatForPrompt(ctx, session.UserID, session.ProjectID, userInput, 8)
		if memBlock != "" {
			sys += "\n" + memBlock
			publish(&Event{Type: EventThought, Content: "memory injected", Timestamp: now()})
			telemetry.IncMemoryRead()
		}
	}
	if taskPlan != nil {
		sys += "\n" + taskPlan.StringForPrompt()
	}

	// Inject spec content (spec.md + tasks.md + checklist.md + CLAUDE.md)
	if l.specSvc != nil {
		specSec := l.specSvc.PromptSection()
		if specSec != "" {
			sys += "\n" + specSec
			publish(&Event{Type: EventThought, Content: "spec injected", Timestamp: now()})
			telemetry.TraceEvent(map[string]any{"event": "spec_injected", "session": session.ID, "has_spec": l.specSvc.HasSpec(), "has_claude": l.specSvc.HasCLAUDE()})
		}
	}

	messages := mapsToChat(history)
	promptUser := userInput
	if continuing {
		promptUser = "Continue: execute any approved pending tool or finish the task from prior context."
	}
	messages = append(messages, port.ChatMessage{Role: "user", Content: promptUser})

	publish(NewEvent(EventThought, 0, "ReAct start: "+truncate(userInput, 80)))

	totalTokens, totalTools := 0, 0
	var final string
	var pending *security.PendingConfirm
	lastSig, same := "", 0
	toolFailStreak := 0

	// resume approved tool �?Observation, then continue ReAct
	if l.perm != nil && continuing {
		if r := l.perm.TakeReadyResume(session.ID); r != nil {
			publish(&Event{Type: EventResume, SubType: r.Tool, Content: "resume " + r.Tool, Timestamp: now()})
			t0 := time.Now()
			resText, _ := l.execTool(ctx, r.Tool, r.Args)
			telemetry.ObserveTool(time.Since(t0))
			resText = l.maybeOffload(ctx, session.ID, r.Tool, resText)
			totalTools++
			obs := FormatObservation(r.Tool, resText)
			publish(&Event{Type: EventToolCall, SubType: r.Tool, Content: r.Tool, Data: r.Args, Timestamp: now()})
			publish(&Event{Type: EventObservation, SubType: r.Tool, Content: truncate(resText, 800), Timestamp: now()})
			publish(&Event{Type: EventToolResult, SubType: r.Tool, Content: truncate(resText, 800), Timestamp: now()})
			auditLog("tool_call", r.Tool, truncate(resText, 200), "resume", time.Since(t0).Milliseconds())
			saveMsg(&sessmodel.Message{
				ID: id("msg"), SessionID: session.ID, Role: "tool", Content: resText,
				ToolName: r.Tool, ToolCallID: "resume", CreatedAt: time.Now(),
				Priority: messagePriority("tool", resText),
			})
			messages = append(messages,
				port.ChatMessage{Role: "assistant", Content: "Thought: resume approved tool\nAction: " + fmt.Sprintf(`{"name":%q,"args":%s}`, r.Tool, mustJSON(r.Args))},
				port.ChatMessage{Role: "tool", Content: obs, Name: r.Tool, ToolCallID: "resume"},
				port.ChatMessage{Role: "user", Content: FormatReActContinue(0, "Approved tool executed. Continue with Thought then Action or Final Answer.")},
			)
			if taskPlan != nil {
				taskPlan.Advance(!isToolFail(resText), "resume")
			}
		}
	}

	for step := 1; step <= l.maxRounds; step++ {
		select {
		case <-ctx.Done():
			return &Result{SessionID: session.ID, Response: "cancelled", ErrorClass: "cancel"}, ctx.Err()
		default:
		}

		// Active token budget via TokenManager
		if l.tokens != nil && l.tokens.Pressure(totalTokens, messages, sys) {
			publish(&Event{Type: EventCompress, Content: fmt.Sprintf("token budget pressure used=%d budget=%d", totalTokens, l.tokenBudget), Timestamp: now()})
			if l.hooks != nil {
				l.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
			}
			if len(messages) > 10 {
				messages = l.tokens.TrimMessages(messages, 6)
				telemetry.IncCompress()
				auditLog("compress", "", "mid_loop_budget", "ok", 0)
			}
			if l.tokens.Exhausted(totalTokens) {
				final = fmt.Sprintf("stopped: token budget exhausted (used=%d budget=%d)", totalTokens, l.tokenBudget)
				publish(&Event{Type: EventError, SubType: "budget", Content: final, Completed: true, Timestamp: now()})
				break
			}
		}

		tLLM := time.Now()
		_, llmSpan := telemetry.StartSpan(ctx, "llm.generate", map[string]string{
			"step": fmt.Sprintf("%d", step),
		})
		resp, err := l.llm.Generate(ctx, &port.ChatRequest{
			SystemPrompt: sys,
			Messages:     messages,
			Temperature:  DefaultTemperature,
		})
		llmSpan.End()
		telemetry.ObserveLLM(time.Since(tLLM))
		if err != nil {
			publish(&Event{Type: EventError, SubType: "llm", Content: err.Error(), Completed: true, Timestamp: now()})
			auditLog("error", "llm", err.Error(), "fail", time.Since(tLLM).Milliseconds())
			return &Result{SessionID: session.ID, Response: "LLM error: " + err.Error(), ErrorClass: "llm", TokenUsed: totalTokens}, nil
		}
		if resp.TotalTokens > 0 {
			totalTokens += resp.TotalTokens
		} else {
			totalTokens += common.EstimateTokens(resp.Content)
		}

		// --- ReAct parse: Thought + Action(s) | Final Answer ---
		react := ParseReAct(resp.Content, resp.ToolCalls)
		if react.Thought != "" {
			publish(&Event{Type: EventThought, Step: step, Content: react.Thought, Timestamp: now()})
		} else {
			publish(NewEvent(EventThought, step, fmt.Sprintf("step %d (implicit)", step)))
		}

		calls := react.Actions
		if len(calls) == 0 {
			final = strings.TrimSpace(react.FinalAnswer)
			if final == "" {
				final = strings.TrimSpace(resp.Content)
			}
			if final == "" {
				final = "done."
			}
			// Plan reviewer before accepting final
			if taskPlan != nil {
				pass, gaps := taskPlan.Review()
				if !pass && step < l.maxRounds {
					msg := "Plan review: incomplete steps �?" + strings.Join(gaps, "; ") +
						". Emit Thought then Action for remaining work, or Final Answer explaining why skipped."
					publish(&Event{Type: EventReview, Content: msg, Data: taskPlan, Timestamp: now()})
					auditLog("review", "", msg, "retry", 0)
					messages = append(messages,
						port.ChatMessage{Role: "assistant", Content: resp.Content},
						port.ChatMessage{Role: "user", Content: msg},
					)
					am := sessmodel.NewMessage(id("msg"), session.ID, "assistant", final)
					am.Priority = messagePriority("assistant", final)
					saveMsg(am)
					continue
				}
				publish(&Event{Type: EventReview, Content: "plan review pass", Data: taskPlan, Timestamp: now()})
			}
			for _, chunk := range chunkText(final, 40) {
				publish(&Event{Type: EventTextDelta, Content: chunk, Timestamp: now()})
			}
			am := sessmodel.NewMessage(id("msg"), session.ID, "assistant", final)
			am.Priority = messagePriority("assistant", final)
			am.TokenCount = common.EstimateTokens(final)
			saveMsg(am)
			session.AddTokens(am.TokenCount)
			publish(&Event{Type: EventAnswer, Content: final, Completed: true, Timestamp: now()})
			break
		}

		sig := toolSig(calls)
		if sig == lastSig {
			same++
			if same >= 2 {
				ref := l.reflect(ctx, "repeated identical tool calls", lastSig)
				publish(&Event{Type: EventReflect, Content: ref, Timestamp: now()})
				telemetry.IncReflect()
				final = "stopped: repeated tool calls\n" + ref
				publish(&Event{Type: EventError, SubType: "loop", Content: final, Completed: true, Timestamp: now()})
				break
			}
		} else {
			same, lastSig = 0, sig
		}

		// Persist assistant turn as Thought+Action for history
		asst := resp.Content
		if asst == "" {
			asst = "Thought: " + react.Thought + "\nAction: " + mustJSON(calls)
		}
		messages = append(messages, port.ChatMessage{Role: "assistant", Content: asst})
		am := sessmodel.NewMessage(id("msg"), session.ID, "assistant", asst)
		am.Priority = messagePriority("assistant", asst)
		am.TokenCount = common.EstimateTokens(asst)
		saveMsg(am)

		// Batch tools: parallel read-only, serial writes; validate + skill + hook abort + permission
		outcomes, p, needBreak := l.runToolCalls(ctx, session, step, calls, skillForTools, opts.AutoApprove, publish, auditLog)
		totalTools += len(outcomes)
		if p != nil {
			pending = p
			final = fmt.Sprintf("CONFIRM required tool=%s id=%s", p.Tool, p.ID)
		}
		var advance func(bool, string)
		if taskPlan != nil {
			advance = func(ok bool, note string) { taskPlan.Advance(ok, note) }
		}
		streak, _ := l.applyOutcomes(ctx, session, step, outcomes, &messages, auditLog, publish, advance)
		if streak > toolFailStreak {
			toolFailStreak = streak
		}
		if needBreak {
			break
		}
		planHint := ""
		if taskPlan != nil {
			planHint = taskPlan.StringForPrompt()
		}
		messages = append(messages, port.ChatMessage{Role: "user", Content: FormatReActContinue(step, planHint)})
	}

	if final == "" {
		final = "no final answer"
	}
	session.AddTokens(totalTokens)
	if err := saveSess(); err != nil {
		// persistence failure is serious but still return response to client
		publish(&Event{Type: EventError, SubType: "persist", Content: "session save failed: " + err.Error(), Timestamp: now()})
	}
	telemetry.AddTokens(int64(totalTokens))
	if droppedEvents > 0 {
		telemetry.Warnf("session=%s sse dropped_events=%d", session.ID, droppedEvents)
	}
	telemetry.TraceEvent(map[string]any{
		"event": "done", "session": session.ID, "tools": totalTools, "tokens": totalTokens, "sseDropped": droppedEvents,
	})
	if l.hooks != nil {
		l.hooks.Emit(ctx, hook.Event{Point: hook.SessionEnd, SessionID: session.ID, Meta: map[string]any{"tokenUsed": totalTokens}})
	}
	publish(&Event{Type: EventDone, Content: final, Completed: true, Data: map[string]any{"tokenUsed": totalTokens, "toolCalls": totalTools, "sseDropped": droppedEvents}, Timestamp: now()})

	res := &Result{
		SessionID: session.ID, Response: final, Steps: totalTools + 1,
		ToolCalls: totalTools, TokenUsed: totalTokens,
	}
	if pending != nil {
		res.NeedPermission = true
		res.Pending = pending
		res.ErrorClass = "permission"
	}
	return res, nil
}

func (l *Loop) reflect(ctx context.Context, problem, detail string) string {
	if l.llm == nil {
		return "Tool failed; try a different path or tool."
	}
	resp, err := l.llm.Generate(ctx, &port.ChatRequest{
		SystemPrompt: "You are a coding agent reflector. Given a failure, output 2-4 short bullets: root cause, next action, which tool to use. No JSON tool calls.",
		Messages: []port.ChatMessage{{
			Role: "user", Content: "Problem: " + problem + "\nDetail: " + detail,
		}},
		Temperature: DefaultTemperature,
		MaxTokens:   ReflectMaxTokens,
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "Failure analysis: check path/args; try glob/grep to locate targets; avoid repeating the same call."
	}
	return strings.TrimSpace(resp.Content)
}

func (l *Loop) execTool(ctx context.Context, name string, args map[string]any) (string, string) {
	t := l.tools.Get(name)
	if t == nil {
		return fmt.Sprintf("tool not found: %s", name), "tool"
	}
	r, err := t.Execute(ctx, args)
	if err != nil {
		return err.Error(), "tool"
	}
	if r.IsError {
		return r.Text, "tool"
	}
	return r.Text, ""
}

func (l *Loop) Permission() *security.Guard { return l.perm }

func isToolFail(s string) bool {
	ls := strings.ToLower(s)
	return strings.Contains(ls, "error") || strings.Contains(ls, "failed") ||
		strings.Contains(s, "失败") || strings.Contains(s, "不存在") ||
		strings.Contains(s, "not found") || strings.Contains(s, "DENIED") ||
		strings.HasPrefix(s, "tool not found")
}

func mapsToChat(history []map[string]any) []port.ChatMessage {
	out := make([]port.ChatMessage, 0, len(history))
	for _, m := range history {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if role == "" || content == "" || role == "system" {
			continue
		}
		cm := port.ChatMessage{Role: role, Content: content}
		if n, ok := m["toolName"].(string); ok {
			cm.Name = n
		}
		if id, ok := m["toolCallId"].(string); ok {
			cm.ToolCallID = id
		}
		out = append(out, cm)
	}
	return out
}

func parseToolCalls(response string) []port.ToolCall {
	response = strings.TrimSpace(response)
	if response == "" {
		return nil
	}
	if i := strings.Index(response, "```"); i >= 0 {
		rest := response[i+3:]
		if strings.HasPrefix(strings.ToLower(rest), "json") {
			rest = rest[4:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			response = strings.TrimSpace(rest[:j])
		}
	}
	var single struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(response), &single); err == nil && single.Name != "" {
		if single.Args == nil {
			single.Args = map[string]any{}
		}
		return []port.ToolCall{{Name: single.Name, Args: single.Args}}
	}
	var multi []struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(response), &multi); err == nil {
		var calls []port.ToolCall
		for _, m := range multi {
			if m.Name != "" {
				if m.Args == nil {
					m.Args = map[string]any{}
				}
				calls = append(calls, port.ToolCall{Name: m.Name, Args: m.Args})
			}
		}
		if len(calls) > 0 {
			return calls
		}
	}
	if i := strings.Index(response, "{"); i >= 0 {
		if j := strings.LastIndex(response, "}"); j > i {
			return parseToolCalls(response[i : j+1])
		}
	}
	return nil
}

func formatTools(list []map[string]string) string {
	var b strings.Builder
	for _, t := range list {
		b.WriteString("- ")
		b.WriteString(t["name"])
		b.WriteString(": ")
		b.WriteString(t["description"])
		b.WriteString("\n")
	}
	return b.String()
}

func isContinue(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "继续" || s == "continue" || s == "ok" || s == "y" || s == "yes" || s == "继续执行"
}

func budget(s string) string {
	return common.TruncateRunes(s, maxToolResultChars)
}

func truncate(s string, n int) string {
	return common.TruncateRunes(s, n)
}

func toolSig(calls []port.ToolCall) string {
	h := sha256.New()
	for _, c := range calls {
		h.Write([]byte(c.Name))
		b, _ := json.Marshal(c.Args)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func ensureID(tc port.ToolCall) string {
	if tc.ID != "" {
		return tc.ID
	}
	h := sha256.Sum256([]byte(tc.Name + mustJSON(tc.Args)))
	return "call_" + hex.EncodeToString(h[:6])
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func id(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

func now() int64 { return time.Now().UnixMilli() }

func chunkText(s string, size int) []string {
	r := []rune(s)
	if size <= 0 {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(r); i += size {
		j := i + size
		if j > len(r) {
			j = len(r)
		}
		out = append(out, string(r[i:j]))
	}
	return out
}

func estimateMaps(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		if c, ok := m["content"].(string); ok {
			n += common.EstimateTokens(c)
		}
	}
	return n
}

func toolsFingerprint(reg *tool.MapRegistry) string {
	if reg == nil {
		return ""
	}
	list := reg.Descriptions()
	// stable enough for cache invalidation when tools change
	var b strings.Builder
	for _, t := range list {
		b.WriteString(t["name"])
		b.WriteByte(',')
	}
	return b.String()
}
