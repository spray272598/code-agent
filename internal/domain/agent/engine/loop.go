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
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

const maxToolResultChars = 4000

type Loop struct {
	llm          port.ILLMPort
	tools        *tool.MapRegistry
	sessions     sessrepo.ISessionRepository
	messages     sessrepo.IMessageRepository
	perm         *security.Guard
	compressor   *contextx.Compressor
	skills       *skill.Service
	hooks        *hook.Bus
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
	return &Loop{
		llm: llm, tools: tools, sessions: sessions, messages: messages,
		perm: perm, compressor: contextx.NewCompressor(tokenBudget / 2),
		maxRounds: maxRounds, tokenBudget: tokenBudget,
		systemPrompt: defaultSystem(),
	}
}

func (l *Loop) SetSkills(s *skill.Service) { l.skills = s }
func (l *Loop) SetHooks(h *hook.Bus)       { l.hooks = h }

func defaultSystem() string {
	return `You are Code-Agent, a coding agent like Claude Code.
You work inside a sandboxed project workspace.
Core tools: read_file, write_file, edit_file, bash, glob, grep.
When you need a tool, reply with ONLY JSON:
{"name":"tool_name","args":{...}}
Or multiple: [{"name":"...","args":{...}}]
When done, answer the user in natural language without JSON tool format.
Be concise. Prefer edit_file over full write when changing existing files.
Dangerous operations will require user confirmation.`
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

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return nil, fmt.Errorf("empty input")
	}
	continuing := isContinue(userInput)

	if l.hooks != nil {
		l.hooks.Emit(ctx, hook.Event{Point: hook.SessionStart, SessionID: session.ID})
	}

	// skill match
	var activeSkill *skill.Skill
	if l.skills != nil && !continuing {
		activeSkill = l.skills.Match(userInput)
		if activeSkill != nil {
			publish(&Event{Type: EventSkill, SubType: activeSkill.ID, Content: "skill: " + activeSkill.Name, Data: activeSkill, Timestamp: now()})
		}
	}

	// save user message
	_ = l.messages.Save(ctx, sessmodel.NewMessage(id("msg"), session.ID, "user", userInput))
	session.AddTokens(common.EstimateTokens(userInput))

	history, _ := l.messages.ListAsMaps(ctx, session.ID, 100)
	if opts.ForceCompact || l.compressor.Needs(history) {
		if l.hooks != nil {
			l.hooks.Emit(ctx, hook.Event{Point: hook.PreCompact, SessionID: session.ID})
		}
		var saved int
		history, saved = l.compressor.Compress(history)
		publish(&Event{Type: EventCompress, Content: fmt.Sprintf("compressed history, ~%d tokens saved", saved), Timestamp: now()})
	}

	toolDesc := l.tools.Descriptions()
	sys := l.systemPrompt + "\n\n## Available tools\n" + formatTools(toolDesc)
	if activeSkill != nil && l.skills != nil {
		sys += "\n" + l.skills.PromptSection(activeSkill)
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

	// resume approved tool
	if l.perm != nil && continuing {
		if r := l.perm.TakeReadyResume(session.ID); r != nil {
			publish(&Event{Type: EventResume, SubType: r.Tool, Content: "resume " + r.Tool, Timestamp: now()})
			resText, errClass := l.execTool(ctx, r.Tool, r.Args)
			totalTools++
			publish(&Event{Type: EventToolCall, SubType: r.Tool, Content: r.Tool, Data: r.Args, Timestamp: now()})
			publish(&Event{Type: EventToolResult, SubType: r.Tool, Content: truncate(resText, 800), Timestamp: now()})
			_ = l.messages.Save(ctx, &sessmodel.Message{
				ID: id("msg"), SessionID: session.ID, Role: "tool", Content: resText,
				ToolName: r.Tool, ToolCallID: "resume", CreatedAt: time.Now(),
			})
			messages = append(messages,
				port.ChatMessage{Role: "assistant", Content: fmt.Sprintf(`{"name":%q,"args":%s}`, r.Tool, mustJSON(r.Args))},
				port.ChatMessage{Role: "tool", Content: resText, Name: r.Tool, ToolCallID: "resume"},
				port.ChatMessage{Role: "user", Content: "Tool executed after approval. Continue the task."},
			)
			_ = errClass
		}
	}

	for step := 1; step <= l.maxRounds; step++ {
		select {
		case <-ctx.Done():
			return &Result{SessionID: session.ID, Response: "cancelled", ErrorClass: "cancel"}, ctx.Err()
		default:
		}
		publish(NewEvent(EventThought, step, fmt.Sprintf("step %d", step)))

		resp, err := l.llm.Generate(ctx, &port.ChatRequest{
			SystemPrompt: sys,
			Messages:     messages,
			Temperature:  0.2,
		})
		if err != nil {
			publish(&Event{Type: EventError, SubType: "llm", Content: err.Error(), Completed: true, Timestamp: now()})
			return &Result{SessionID: session.ID, Response: "LLM error: " + err.Error(), ErrorClass: "llm", TokenUsed: totalTokens}, nil
		}
		if resp.TotalTokens > 0 {
			totalTokens += resp.TotalTokens
		} else {
			totalTokens += common.EstimateTokens(resp.Content)
		}

		// stream-like text for final answers
		calls := resp.ToolCalls
		if len(calls) == 0 {
			calls = parseToolCalls(resp.Content)
		}
		if len(calls) == 0 {
			final = strings.TrimSpace(resp.Content)
			if final == "" {
				final = "done."
			}
			// emit as text deltas in chunks for CLI feel
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
				final = "stopped: repeated tool calls\n" + resp.Content
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
					publish(&Event{Type: EventPermission, SubType: "deny", Content: msg, Data: dec, Timestamp: now()})
					messages = append(messages, port.ChatMessage{Role: "tool", Content: msg, Name: tc.Name, ToolCallID: ensureID(tc)})
					continue
				case security.ActionConfirm:
					p := l.perm.CreatePending(session.ID, tc.Name, tc.Args, dec)
					pending = p
					msg := fmt.Sprintf("CONFIRM required [%s] tool=%s reason=%s id=%s\nApprove via CLI or POST /api/v1/permission/approve then send 继续",
						dec.Layer, tc.Name, dec.Reason, p.ID)
					publish(&Event{Type: EventPermission, SubType: "confirm", Content: msg, Data: p, Completed: true, Timestamp: now()})
					final = msg
					needBreak = true
					break
				}
			}
			if l.hooks != nil {
				l.hooks.Emit(ctx, hook.Event{Point: hook.PreToolUse, SessionID: session.ID, Tool: tc.Name, Args: tc.Args})
			}
			publish(&Event{Type: EventToolCall, SubType: tc.Name, Step: step, Content: tc.Name, Data: tc.Args, Timestamp: now()})
			resText, _ := l.execTool(ctx, tc.Name, tc.Args)
			resText = budget(resText)
			if l.hooks != nil {
				l.hooks.Emit(ctx, hook.Event{Point: hook.PostToolUse, SessionID: session.ID, Tool: tc.Name, Args: tc.Args, Result: resText})
			}
			publish(&Event{Type: EventToolResult, SubType: tc.Name, Step: step, Content: truncate(resText, 800), Timestamp: now()})
			callID := ensureID(tc)
			_ = l.messages.Save(ctx, &sessmodel.Message{
				ID: id("msg"), SessionID: session.ID, Role: "tool", Content: resText,
				ToolName: tc.Name, ToolCallID: callID, Step: step, CreatedAt: time.Now(),
			})
			messages = append(messages, port.ChatMessage{Role: "tool", Content: resText, Name: tc.Name, ToolCallID: callID})
		}
		if needBreak {
			break
		}
		messages = append(messages, port.ChatMessage{Role: "user", Content: fmt.Sprintf("Step %d done. Continue or answer.", step)})
	}

	if final == "" {
		final = "no final answer"
	}
	session.AddTokens(totalTokens)
	_ = l.sessions.Save(ctx, session)
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

// helpers

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
	// strip markdown fences
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
