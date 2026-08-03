package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"
)

// skillAllows returns false if active skill constrains tools and name is not allowed.
func skillAllows(sk *skill.Skill, name string) bool {
	if sk == nil || len(sk.Tools) == 0 {
		return true
	}
	base := name
	if i := strings.LastIndex(name, "__"); i >= 0 {
		base = name[i+2:]
	}
	for _, t := range sk.Tools {
		if t == name || t == base || t == "*" {
			return true
		}
		// prefix match e.g. "read*"
		if strings.HasSuffix(t, "*") && strings.HasPrefix(base, strings.TrimSuffix(t, "*")) {
			return true
		}
	}
	return false
}

// messagePriority infers priority like walicode PriorityReducer.
func messagePriority(role, content string) int {
	lc := strings.ToLower(content)
	if role == "tool" && (strings.Contains(lc, "error") || strings.Contains(lc, "failed") ||
		strings.Contains(lc, "denied") || strings.Contains(content, "DENIED")) {
		return 9 // critical
	}
	if role == "user" {
		return 5
	}
	if role == "system" {
		return 6
	}
	if role == "assistant" && len([]rune(content)) > 5000 {
		return 2 // low — compressible
	}
	if role == "tool" {
		return 4
	}
	return 3
}

type toolOutcome struct {
	tc      port.ToolCall
	text    string
	failed  bool
	denied  bool
	latency time.Duration
	aborted bool
	cached  bool
}

// runToolCalls executes a batch of tool calls.
// Read-only tools run in parallel (walicode ToolConcurrencyController); writes serial.
// Returns needBreak=true when permission confirm is required.
func (l *Loop) runToolCalls(
	ctx context.Context,
	session *sessmodel.Session,
	step int,
	calls []port.ToolCall,
	activeSkill *skill.Skill,
	autoApprove bool,
	publish func(*Event),
	auditLog func(action, toolName, detail, decision string, latencyMs int64),
) (outcomes []toolOutcome, pending *security.PendingConfirm, needBreak bool) {
	// --- serial gate: skill + validate + permission + PreToolUse abort ---
	type ready struct {
		tc    port.ToolCall
		block string // non-empty => skip execute with this observation
	}
	var queue []ready
	for _, tc := range calls {
		publish(&Event{Type: EventAction, SubType: tc.Name, Step: step, Content: tc.Name, Data: tc.Args, Timestamp: now()})

		if !skillAllows(activeSkill, tc.Name) {
			msg := fmt.Sprintf("BLOCKED by skill %s: tool %s not in allowed list %v", activeSkill.ID, tc.Name, activeSkill.Tools)
			queue = append(queue, ready{tc: tc, block: msg})
			publish(&Event{Type: EventObservation, SubType: tc.Name, Content: msg, Step: step, Timestamp: now()})
			continue
		}

		// schema validation
		if t := l.tools.Get(tc.Name); t != nil {
			if err := tool.ValidateArgs(t.InputSchema(), tc.Args); err != nil {
				msg := "validation error: " + err.Error()
				queue = append(queue, ready{tc: tc, block: msg})
				publish(&Event{Type: EventObservation, SubType: tc.Name, Content: msg, Step: step, Timestamp: now()})
				continue
			}
		}

		if l.perm != nil && !autoApprove {
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
				queue = append(queue, ready{tc: tc, block: msg})
				continue
			case security.ActionConfirm:
				p := l.perm.CreatePending(session.ID, tc.Name, tc.Args, dec)
				pending = p
				msg := fmt.Sprintf("CONFIRM required [%s] tool=%s reason=%s id=%s\nApprove via CLI or POST /api/v1/permission/approve then send 继续",
					dec.Layer, tc.Name, dec.Reason, p.ID)
				publish(&Event{Type: EventPermission, SubType: "confirm", Content: msg, Data: p, Completed: true, Timestamp: now()})
				auditLog("permission", tc.Name, dec.Reason, "confirm", 0)
				needBreak = true
				// flush any prior blocked as outcomes first
				for _, q := range queue {
					if q.block != "" {
						outcomes = append(outcomes, toolOutcome{tc: q.tc, text: q.block, failed: true, denied: strings.HasPrefix(q.block, "DENIED")})
					}
				}
				return outcomes, pending, true
			}
		}

		if l.hooks != nil {
			if aborted, err := l.hooks.EmitCheck(ctx, hook.Event{Point: hook.PreToolUse, SessionID: session.ID, Tool: tc.Name, Args: tc.Args}); aborted {
				msg := "HOOK_ABORT: " + err.Error()
				publish(&Event{Type: EventObservation, SubType: tc.Name, Content: msg, Step: step, Timestamp: now()})
				auditLog("hook_abort", tc.Name, msg, "abort", 0)
				queue = append(queue, ready{tc: tc, block: msg})
				continue
			}
		}
		queue = append(queue, ready{tc: tc})
	}

	// Materialize blocked first in order, execute others
	var toRun []ready
	for _, q := range queue {
		if q.block != "" {
			outcomes = append(outcomes, toolOutcome{tc: q.tc, text: q.block, failed: true, denied: strings.HasPrefix(q.block, "DENIED") || strings.HasPrefix(q.block, "HOOK_ABORT") || strings.HasPrefix(q.block, "BLOCKED") || strings.HasPrefix(q.block, "validation")})
			continue
		}
		toRun = append(toRun, q)
	}
	if len(toRun) == 0 {
		return outcomes, nil, false
	}

	allRead := true
	for _, q := range toRun {
		if !tool.IsReadOnly(q.tc.Name) {
			allRead = false
			break
		}
	}

	runOne := func(tc port.ToolCall) toolOutcome {
		out := toolOutcome{tc: tc}
		publish(&Event{Type: EventToolCall, SubType: tc.Name, Step: step, Content: tc.Name, Data: tc.Args, Timestamp: now()})
		observability.Global.ToolCalls.Add(1)

		// cache hit
		if l.toolCache != nil {
			if hit, ok := l.toolCache.Get(tc.Name, tc.Args); ok {
				out.text = hit
				out.cached = true
				out.latency = 0
				publish(&Event{Type: EventToolResult, SubType: tc.Name, Step: step, Content: truncate(hit, 800) + " [cache]", Timestamp: now()})
				return out
			}
		}

		t0 := time.Now()
		var resText string
		_ = observability.SpanTool(ctx, tc.Name, func(tctx context.Context) error {
			resText, _ = l.execTool(tctx, tc.Name, tc.Args)
			return nil
		})
		out.latency = time.Since(t0)
		observability.Global.ObserveTool(out.latency)
		resText = l.maybeOffload(ctx, session.ID, tc.Name, resText)
		out.text = resText
		out.failed = isToolFail(resText)
		if l.toolCache != nil && !out.failed {
			l.toolCache.Put(tc.Name, tc.Args, resText)
		}
		if l.hooks != nil {
			l.hooks.Emit(ctx, hook.Event{Point: hook.PostToolUse, SessionID: session.ID, Tool: tc.Name, Args: tc.Args, Result: resText})
		}
		publish(&Event{Type: EventObservation, SubType: tc.Name, Step: step, Content: truncate(resText, 800), Timestamp: now()})
		publish(&Event{Type: EventToolResult, SubType: tc.Name, Step: step, Content: truncate(resText, 800), Timestamp: now()})
		return out
	}

	if allRead && len(toRun) > 1 {
		// parallel read-only batch (preserve order of outcomes)
		results := make([]toolOutcome, len(toRun))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 5) // max 5 concurrent like walicode
		for i, q := range toRun {
			wg.Add(1)
			go func(i int, tc port.ToolCall) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i] = runOne(tc)
			}(i, q.tc)
		}
		wg.Wait()
		outcomes = append(outcomes, results...)
	} else {
		for _, q := range toRun {
			outcomes = append(outcomes, runOne(q.tc))
		}
	}
	return outcomes, nil, false
}

// applyOutcomes persists tool messages and builds chat history fragments.
func (l *Loop) applyOutcomes(
	ctx context.Context,
	session *sessmodel.Session,
	step int,
	outcomes []toolOutcome,
	messages *[]port.ChatMessage,
	auditLog func(action, toolName, detail, decision string, latencyMs int64),
	publish func(*Event),
	taskAdvance func(ok bool, note string),
) (toolFailStreak int, anyFail bool) {
	for _, o := range outcomes {
		obs := FormatObservation(o.tc.Name, o.text)
		callID := ensureID(o.tc)
		pri := messagePriority("tool", o.text)
		if err := l.messages.Save(ctx, &sessmodel.Message{
			ID: id("msg"), SessionID: session.ID, Role: "tool", Content: o.text,
			ToolName: o.tc.Name, ToolCallID: callID, Step: step, Priority: pri, CreatedAt: time.Now(),
			TokenCount: common.EstimateTokens(o.text),
		}); err != nil {
			observability.Warnf("tool message save: %v", err)
		}
		*messages = append(*messages, port.ChatMessage{Role: "tool", Content: obs, Name: o.tc.Name, ToolCallID: callID})
		decision := "ok"
		if o.failed {
			decision = "error"
			anyFail = true
			toolFailStreak++
			if taskAdvance != nil {
				taskAdvance(false, truncate(o.text, 80))
			}
		} else {
			toolFailStreak = 0
			if taskAdvance != nil {
				taskAdvance(true, o.tc.Name)
			}
		}
		if o.cached {
			decision = "cache"
		}
		auditLog("tool_call", o.tc.Name, truncate(o.text, 300), decision, o.latency.Milliseconds())

		if o.failed {
			ref := l.reflect(ctx, fmt.Sprintf("tool %s failed: %s", o.tc.Name, truncate(o.text, 200)), mustJSON(o.tc.Args))
			publish(&Event{Type: EventReflect, Content: ref, Step: step, Timestamp: now()})
			observability.Global.ReflectTotal.Add(1)
			auditLog("reflect", o.tc.Name, ref, "ok", 0)
			*messages = append(*messages, port.ChatMessage{Role: "user", Content:
				"Observation indicated failure. Next turn MUST start with Thought diagnosing the failure, then Action or Final Answer.\n" +
					"Failure analysis:\n" + ref})
		}
	}
	return toolFailStreak, anyFail
}

// filterToolDescriptions for skill-constrained prompts.
func filterToolDescriptions(all []map[string]string, sk *skill.Skill) []map[string]string {
	if sk == nil || len(sk.Tools) == 0 {
		return all
	}
	var out []map[string]string
	for _, t := range all {
		if skillAllows(sk, t["name"]) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

// estimateMessageTokens estimates tokens in the in-flight message list.
func estimateMessageTokens(msgs []port.ChatMessage, sys string) int {
	n := common.EstimateTokens(sys)
	for _, m := range msgs {
		n += common.EstimateTokens(m.Content)
	}
	return n
}
