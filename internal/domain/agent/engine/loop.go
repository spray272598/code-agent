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
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"
)

const maxToolResultChars = 4000

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
	memSvc       *memory.Service
	memCtx       *coding.MemoryContext
	audit        audit.Repository
	maxRounds    int
	tokenBudget  int
	systemPrompt string
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
		maxRounds = 20
	}
	if tokenBudget <= 0 {
		tokenBudget = 32000
	}
	comp := contextx.NewCompressor(tokenBudget / 2)
	comp.SetSummarizer(contextx.NewSummarizer(llm))
	return &Loop{
		llm: llm, tools: tools, sessions: sessions, messages: messages,
		perm: perm, compressor: comp,
		maxRounds: maxRounds, tokenBudget: tokenBudget,
		systemPrompt: defaultSystem(),
	}
}

func (l *Loop) SetSkills(s *skill.Service) { l.skills = s }
func (l *Loop) SetHooks(h *hook.Bus)       { l.hooks = h }
func (l *Loop) SetMemory(svc *memory.Service, mc *coding.MemoryContext) {
	l.memSvc = svc
	l.memCtx = mc
}
func (l *Loop) SetAudit(a audit.Repository)                    { l.audit = a }
func (l *Loop) SetSummaryRepo(s sessrepo.ISummaryRepository)   { l.summaries = s }

func defaultSystem() string {
	return `You are Code-Agent, a coding agent like Claude Code.
You work inside a sandboxed project workspace.
Core tools: read_file, write_file, edit_file, bash, glob, grep, memory_save, memory_search.
Use memory_save for durable user/project facts; memory_search to recall them.
When you need a tool, reply with ONLY JSON:
{"name":"tool_name","args":{...}}
Or multiple: [{"name":"...","args":{...}}]
When done, answer the user in natural language without JSON tool format.
Be concise. Prefer edit_file over full write when changing existing files.
Dangerous operations will require user confirmation.
If a tool fails, adjust strategy (different path, tool, or ask user).`
}

type RunOptions struct {
	AutoApprove  bool
	ForceCompact bool
}

func (l *Loop) Run(ctx context.Context, session *sessmodel.Session, userInput string, eventCh chan<- *Event, opts RunOptions) (*Result, error) {
	publish := func(ev *Event) {
		if eventCh == nil || ev == nil {
			return
		}
		select {
		case eventCh <- ev:
		default:
		}
	}
	auditLog := func(action, toolName, detail, decision string, latencyMs int64) {
		if l.audit == nil {
			return
		}
		_ = l.audit.Append(ctx, audit.Entry{
			UserID: session.UserID, SessionID: session.ID,
			Action: action, Tool: toolName, Detail: detail, Decision: decision, LatencyMs: latencyMs,
		})
	}

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return nil, fmt.Errorf("empty input")
	}
	continuing := isContinue(userInput)

	if l.hooks != nil {
		l.hooks.Emit(ctx, hook.Event{Point: hook.SessionStart, SessionID: session.ID})
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

	// plan
	var taskPlan *plan.Plan
	if !continuing {
		taskPlan = plan.BuildRulePlan(userInput)
		if taskPlan != nil {
			publish(&Event{Type: EventPlan, Content: taskPlan.Goal, Data: taskPlan, Timestamp: now()})
		}
	}

	_ = l.messages.Save(ctx, sessmodel.NewMessage(id("msg"), session.ID, "user", userInput))
	session.AddTokens(common.EstimateTokens(userInput))

	history, _ := l.messages.ListAsMaps(ctx, session.ID, 120)
	priorSummary := ""
	if l.summaries != nil {
		priorSummary, _ = l.summaries.Get(ctx, session.ID)
	}
	if opts.ForceCompact || l.compressor.Needs(history) {
		if l.hooks != nil {
			l.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
		}
		// L3 summary when history is large or forced
		useSum := opts.ForceCompact || len(history) > 16 || estimateMaps(history) > l.tokenBudget/3
		cr := l.compressor.CompressLevels(ctx, history, priorSummary, useSum)
		history = cr.History
		if cr.Summary != "" && l.summaries != nil {
			_ = l.summaries.Save(ctx, session.ID, cr.Summary, common.EstimateTokens(cr.Summary))
		}
		observability.Global.CompressTotal.Add(1)
		publish(&Event{Type: EventCompress, Content: fmt.Sprintf("compress %s saved~%d", cr.Level, cr.Saved), Data: map[string]any{"level": cr.Level, "summary": cr.Summary != ""}, Timestamp: now()})
		auditLog("compress", "", cr.Level, "ok", 0)
		observability.Trace.Event(map[string]any{"event": "compress", "session": session.ID, "level": cr.Level, "saved": cr.Saved})
	}

	toolDesc := l.tools.Descriptions()
	sys := l.systemPrompt + "\n\n## Available tools\n" + formatTools(toolDesc)
	if activeSkill != nil && l.skills != nil {
		sys += "\n" + l.skills.PromptSection(activeSkill)
	}
	if l.memSvc != nil {
		memBlock := l.memSvc.FormatForPrompt(ctx, session.UserID, session.ProjectID, userInput, 8)
		if memBlock != "" {
			sys += "\n" + memBlock
			publish(&Event{Type: EventThought, Content: "memory injected", Timestamp: now()})
			observability.Global.MemoryReads.Add(1)
		}
	}
	if taskPlan != nil {
		sys += "\n" + taskPlan.StringForPrompt()
	}

	messages := mapsToChat(history)
	promptUser := userInput
	if continuing {
		promptUser = "Continue: execute any approved pending tool or finish the task from prior context."
	}
	messages = append(messages, port.ChatMessage{Role: "user", Content: promptUser})

	publish(NewEvent(EventThought, 0, "processing: "+truncate(userInput, 80)))

	totalTokens, totalTools := 0, 0
	var final string
	var pending *security.PendingConfirm
	lastSig, same := "", 0
	toolFailStreak := 0

	// resume approved tool
	if l.perm != nil && continuing {
		if r := l.perm.TakeReadyResume(session.ID); r != nil {
			publish(&Event{Type: EventResume, SubType: r.Tool, Content: "resume " + r.Tool, Timestamp: now()})
			t0 := time.Now()
			resText, _ := l.execTool(ctx, r.Tool, r.Args)
			observability.Global.ObserveTool(time.Since(t0))
			totalTools++
			publish(&Event{Type: EventToolCall, SubType: r.Tool, Content: r.Tool, Data: r.Args, Timestamp: now()})
			publish(&Event{Type: EventToolResult, SubType: r.Tool, Content: truncate(resText, 800), Timestamp: now()})
			auditLog("tool_call", r.Tool, truncate(resText, 200), "resume", time.Since(t0).Milliseconds())
			_ = l.messages.Save(ctx, &sessmodel.Message{
				ID: id("msg"), SessionID: session.ID, Role: "tool", Content: resText,
				ToolName: r.Tool, ToolCallID: "resume", CreatedAt: time.Now(),
			})
			messages = append(messages,
				port.ChatMessage{Role: "assistant", Content: fmt.Sprintf(`{"name":%q,"args":%s}`, r.Tool, mustJSON(r.Args))},
				port.ChatMessage{Role: "tool", Content: resText, Name: r.Tool, ToolCallID: "resume"},
				port.ChatMessage{Role: "user", Content: "Tool executed after approval. Continue the task."},
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
		publish(NewEvent(EventThought, step, fmt.Sprintf("step %d", step)))

		tLLM := time.Now()
		resp, err := l.llm.Generate(ctx, &port.ChatRequest{
			SystemPrompt: sys,
			Messages:     messages,
			Temperature:  0.2,
		})
		observability.Global.ObserveLLM(time.Since(tLLM))
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

		calls := resp.ToolCalls
		if len(calls) == 0 {
			calls = parseToolCalls(resp.Content)
		}
		if len(calls) == 0 {
			final = strings.TrimSpace(resp.Content)
			if final == "" {
				final = "done."
			}
			// Plan reviewer before accepting final
			if taskPlan != nil {
				pass, gaps := taskPlan.Review()
				if !pass && step < l.maxRounds {
					msg := "Plan review: incomplete steps — " + strings.Join(gaps, "; ") + ". Continue with remaining work or explain why skipped."
					publish(&Event{Type: EventReview, Content: msg, Data: taskPlan, Timestamp: now()})
					auditLog("review", "", msg, "retry", 0)
					messages = append(messages,
						port.ChatMessage{Role: "assistant", Content: final},
						port.ChatMessage{Role: "user", Content: msg},
					)
					_ = l.messages.Save(ctx, sessmodel.NewMessage(id("msg"), session.ID, "assistant", final))
					continue
				}
				publish(&Event{Type: EventReview, Content: "plan review pass", Data: taskPlan, Timestamp: now()})
			}
			for _, chunk := range chunkText(final, 40) {
				publish(&Event{Type: EventTextDelta, Content: chunk, Timestamp: now()})
			}
			_ = l.messages.Save(ctx, sessmodel.NewMessage(id("msg"), session.ID, "assistant", final))
			session.AddTokens(common.EstimateTokens(final))
			publish(&Event{Type: EventAnswer, Content: final, Completed: true, Timestamp: now()})
			break
		}

		sig := toolSig(calls)
		if sig == lastSig {
			same++
			if same >= 2 {
				// reflect then stop if still looping
				ref := l.reflect(ctx, "repeated identical tool calls", lastSig)
				publish(&Event{Type: EventReflect, Content: ref, Timestamp: now()})
				observability.Global.ReflectTotal.Add(1)
				final = "stopped: repeated tool calls\n" + ref
				publish(&Event{Type: EventError, SubType: "loop", Content: final, Completed: true, Timestamp: now()})
				break
			}
		} else {
			same, lastSig = 0, sig
		}

		asst := resp.Content
		if asst == "" {
			asst = mustJSON(calls)
		}
		messages = append(messages, port.ChatMessage{Role: "assistant", Content: asst})
		_ = l.messages.Save(ctx, sessmodel.NewMessage(id("msg"), session.ID, "assistant", asst))

		needBreak := false
		for _, tc := range calls {
			totalTools++
			if l.perm != nil && !opts.AutoApprove {
				dec := l.perm.Check(session.ID, tc.Name, tc.Args)
				if l.hooks != nil {
					l.hooks.Emit(ctx, hook.Event{Point: hook.Permission, SessionID: session.ID, Tool: tc.Name, Args: tc.Args, Decision: string(dec.Action)})
				}
				switch dec.Action {
				case security.ActionDeny:
					msg := fmt.Sprintf("DENIED [%s]: %s", dec.Layer, dec.Reason)
					observability.Global.PermissionDeny.Add(1)
					publish(&Event{Type: EventPermission, SubType: "deny", Content: msg, Data: dec, Timestamp: now()})
					auditLog("permission", tc.Name, dec.Reason, "deny", 0)
					messages = append(messages, port.ChatMessage{Role: "tool", Content: msg, Name: tc.Name, ToolCallID: ensureID(tc)})
					continue
				case security.ActionConfirm:
					p := l.perm.CreatePending(session.ID, tc.Name, tc.Args, dec)
					pending = p
					msg := fmt.Sprintf("CONFIRM required [%s] tool=%s reason=%s id=%s\nApprove via CLI or POST /api/v1/permission/approve then send 继续",
						dec.Layer, tc.Name, dec.Reason, p.ID)
					publish(&Event{Type: EventPermission, SubType: "confirm", Content: msg, Data: p, Completed: true, Timestamp: now()})
					auditLog("permission", tc.Name, dec.Reason, "confirm", 0)
					final = msg
					needBreak = true
					break
				}
			}
			if l.hooks != nil {
				l.hooks.Emit(ctx, hook.Event{Point: hook.PreToolUse, SessionID: session.ID, Tool: tc.Name, Args: tc.Args})
			}
			publish(&Event{Type: EventToolCall, SubType: tc.Name, Step: step, Content: tc.Name, Data: tc.Args, Timestamp: now()})
			observability.Global.ToolCalls.Add(1)
			t0 := time.Now()
			resText, _ := l.execTool(ctx, tc.Name, tc.Args)
			lat := time.Since(t0)
			observability.Global.ObserveTool(lat)
			resText = budget(resText)
			failed := isToolFail(resText)
			if failed {
				toolFailStreak++
			} else {
				toolFailStreak = 0
				if taskPlan != nil {
					taskPlan.Advance(true, tc.Name)
				}
			}
			if l.hooks != nil {
				l.hooks.Emit(ctx, hook.Event{Point: hook.PostToolUse, SessionID: session.ID, Tool: tc.Name, Args: tc.Args, Result: resText})
			}
			publish(&Event{Type: EventToolResult, SubType: tc.Name, Step: step, Content: truncate(resText, 800), Timestamp: now()})
			auditLog("tool_call", tc.Name, truncate(resText, 300), map[bool]string{true: "error", false: "ok"}[failed], lat.Milliseconds())
			callID := ensureID(tc)
			_ = l.messages.Save(ctx, &sessmodel.Message{
				ID: id("msg"), SessionID: session.ID, Role: "tool", Content: resText,
				ToolName: tc.Name, ToolCallID: callID, Step: step, CreatedAt: time.Now(),
			})
			messages = append(messages, port.ChatMessage{Role: "tool", Content: resText, Name: tc.Name, ToolCallID: callID})

			// Reflect after tool failure
			if failed && toolFailStreak >= 1 {
				ref := l.reflect(ctx, fmt.Sprintf("tool %s failed: %s", tc.Name, truncate(resText, 200)), mustJSON(tc.Args))
				publish(&Event{Type: EventReflect, Content: ref, Step: step, Timestamp: now()})
				observability.Global.ReflectTotal.Add(1)
				auditLog("reflect", tc.Name, ref, "ok", 0)
				messages = append(messages, port.ChatMessage{Role: "user", Content: "Reflection:\n" + ref + "\nAdjust and continue."})
				if taskPlan != nil {
					taskPlan.Advance(false, truncate(resText, 80))
				}
			}
		}
		if needBreak {
			break
		}
		stepHint := fmt.Sprintf("Step %d done. Continue or answer.", step)
		if taskPlan != nil {
			stepHint += "\n" + taskPlan.StringForPrompt()
		}
		messages = append(messages, port.ChatMessage{Role: "user", Content: stepHint})
	}

	if final == "" {
		final = "no final answer"
	}
	session.AddTokens(totalTokens)
	_ = l.sessions.Save(ctx, session)
	observability.Global.TokensTotal.Add(int64(totalTokens))
	observability.Trace.Event(map[string]any{
		"event": "done", "session": session.ID, "tools": totalTools, "tokens": totalTokens,
	})
	if l.hooks != nil {
		l.hooks.Emit(ctx, hook.Event{Point: hook.SessionEnd, SessionID: session.ID, Meta: map[string]any{"tokenUsed": totalTokens}})
	}
	publish(&Event{Type: EventDone, Content: final, Completed: true, Data: map[string]any{"tokenUsed": totalTokens, "toolCalls": totalTools}, Timestamp: now()})

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
		Temperature: 0.2,
		MaxTokens:   200,
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
