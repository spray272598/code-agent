package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/agent/plan"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/types/common"
)

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

	// planExplore gates PlanMode context isolation (M5.7-3): while true the loop
	// keeps the working window free of implementation context. Toggled by the
	// ControlPlanExplore / ControlPlanImplement signals below.
	planExplore := false

	if l.hooks != nil {
		l.hooks.Emit(ctx, hook.Event{Point: hook.SessionStart, SessionID: session.ID})
	}
	// wire subagent progress → SSE
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
		l.memSvc.MaybeExtractFromUserCorrection(ctx, session.ProjectID, session.ID, userInput)
	}

	var activeSkill *skill.Skill
	if l.skills != nil && !continuing {
		activeSkill = l.skills.Match(userInput)
		if activeSkill == nil {
			activeSkill = l.skills.MatchSemantic(ctx, userInput)
		}
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
			publish(&Event{Type: EventPlanUpdate, Content: taskPlan.Goal, Data: taskPlan.View(), Timestamp: now()})
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
	// PlanMode context isolation (M5.7-3): during the read-only explore phase we
	// deliberately do NOT accumulate implementation context. The agent should
	// read files/explore (via the guard's read-only tier) but its findings are
	// NOT folded into the working window until we enter the implement phase.
	// This keeps the explore window lean and avoids polluting the plan with
	// exploratory noise. The guard mode is set by the same control signal.
	if planExplore {
		history = nil
		fullLoad = false
		telemetry.TraceEvent(map[string]any{"event": "plan_explore_isolate", "session": session.ID})
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

	// publishPlanUpdate emits a render-ready plan snapshot when a plan exists.
	publishPlanUpdate := func(p *plan.Plan, note string) {
		if p == nil {
			return
		}
		publish(&Event{Type: EventPlanUpdate, Content: note, Data: p.View(), Timestamp: now()})
	}

	// handleControl processes a single mid-run instruction. Returns true if the
	// loop must stop (interrupt/pause boundary). Mutates taskPlan via replanCh.
	replanPending := false
	var newGoal string
	handleControl := func(c Control) bool {
		switch c.Signal {
		case ControlReplan, ControlReplanWithGoal:
			if c.Signal == ControlReplanWithGoal {
				newGoal = strings.TrimSpace(c.Goal)
			}
			replanPending = true
			return false
		case ControlPause:
			publish(&Event{Type: EventCheckpoint, SubType: "paused", Content: "paused for input", Data: taskPlan.View(), Timestamp: now()})
			auditLog("pause", "", "user", "ok", 0)
			// block until resume/interrupt or ctx cancel
			for {
				select {
				case <-ctx.Done():
					return true
				case nc := <-opts.ControlCh:
					if nc.Signal == ControlResume {
						publish(&Event{Type: EventResume, Content: "resumed", Timestamp: now()})
						return false
					}
					if nc.Signal == ControlInterrupt {
						return true
					}
					if nc.Signal == ControlReplan || nc.Signal == ControlReplanWithGoal {
						if nc.Signal == ControlReplanWithGoal {
							newGoal = strings.TrimSpace(nc.Goal)
						}
						replanPending = true
						return false
					}
				}
			}
		case ControlInterrupt:
			return true
		case ControlPlanExplore:
			// enter read-only explore phase: guard denies mutating tools
			if l.perm != nil {
				l.perm.SetMode(security.ModeReadonly)
			}
			// isolate context: stop accumulating implementation history
			planExplore = true
			publish(&Event{Type: EventCheckpoint, SubType: "plan_explore", Content: "plan explore phase (read-only)", Data: taskPlan.View(), Timestamp: now()})
			auditLog("plan", "", "explore", "ok", 0)
			return false
		case ControlPlanImplement:
			// exit plan phase into implement phase: restore writable tier
			if l.perm != nil {
				l.perm.SetMode(security.ModeWorkspace)
			}
			// re-load implementation context: clear isolation so the next round
			// loads the full working window (files read during explore are still
			// available via their own tool calls; we just stop hiding history).
			planExplore = false
			publish(&Event{Type: EventCheckpoint, SubType: "plan_implement", Content: "plan implement phase (writable)", Data: taskPlan.View(), Timestamp: now()})
			auditLog("plan", "", "implement", "ok", 0)
			return false
		default:
			return false
		}
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
		memBlock := l.memSvc.FormatForPrompt(ctx, session.ProjectID, userInput, 8)
		if memBlock != "" {
			sys += "\n" + memBlock
			publish(&Event{Type: EventThought, Content: "memory injected", Timestamp: now()})
			telemetry.IncMemoryRead()
		}
	}
	// sysBase is the system prompt without the plan section, so a mid-run
	// re-plan can rebuild sys cheaply.
	sysBase := sys
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

	// resume approved tool → Observation, then continue ReAct
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

		// Non-blocking drain of pending control signals before this step.
		if opts.ControlCh != nil {
			for {
				select {
				case c := <-opts.ControlCh:
					if handleControl(c) {
						publish(&Event{Type: EventCancel, Content: "interrupted", Completed: true, Timestamp: now()})
						return &Result{SessionID: session.ID, Response: "interrupted", ErrorClass: "cancel"}, ctx.Err()
					}
				default:
					goto contCtrl
				}
			}
		contCtrl:
		}

		// Apply a queued re-plan (user- or failure-triggered).
		if replanPending && taskPlan != nil {
			replanPending = false
			ref := l.reflect(ctx, "re-planning after stall or user request", taskPlan.Goal)
			paused := taskPlan
			taskPlan = paused.Replan(newGoal)
			newGoal = ""
			publish(&Event{Type: EventReplan, Content: ref, Data: taskPlan, Timestamp: now()})
			publishPlanUpdate(taskPlan, "replan")
			// refresh plan in system prompt for subsequent steps
			sys = sysBase + "\n" + taskPlan.StringForPrompt()
			telemetry.IncReflect()
			auditLog("replan", "", ref, "ok", 0)
		}

		// Active token budget via TokenManager
		if l.tokens != nil {
			if l.tokens.Pressure(totalTokens, messages, sys) {
				bs := l.tokens.State()
				publish(&Event{Type: EventCompress, Content: fmt.Sprintf("token budget pressure used=%d budget=%d remaining=%d reserved=%d", totalTokens, l.tokenBudget, bs.Remaining, bs.Reserved), Timestamp: now()})
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
					// If LLM explicitly returned a Final Answer, accept it as final
					// rather than forcing another round. This handles cases where
					// the LLM decides the task is complete despite plan gaps.
					if react.FinalAnswer != "" {
						publish(&Event{Type: EventReview, Content: "plan review: gaps but LLM returned final answer", Data: taskPlan, Timestamp: now()})
						break
					}
					msg := "Plan review: incomplete steps — " + strings.Join(gaps, "; ") +
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
			if same >= 3 {
				ref := l.reflect(ctx, "repeated identical tool calls", lastSig)
				publish(&Event{Type: EventReflect, Content: ref, Timestamp: now()})
				telemetry.IncReflect()
				final = "stopped: repeated tool calls\n" + ref
				publish(&Event{Type: EventError, SubType: "loop", Content: final, Completed: true, Timestamp: now()})
				break
			}
			if same == 1 {
				nudge := fmt.Sprintf(
					"NOTICE: You have called %s with identical arguments twice in a row. "+
						"This is likely a stuck loop. Please STOP repeating this call and "+
						"either diagnose the failure, try a different approach, or provide a Final Answer.",
					calls[0].Name,
				)
				publish(&Event{Type: EventThought, Content: "stationarity nudge", Timestamp: now()})
				messages = append(messages, port.ChatMessage{Role: "user", Content: nudge})
				saveMsg(sessmodel.NewMessage(id("msg"), session.ID, "user", nudge))
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
		// Reserve estimated budget for tool batch results before parallel execution
		var reservedEstimate int
		if l.tokens != nil && len(calls) > 0 {
			reservedEstimate = len(calls) * 200
			if reservedEstimate > l.tokens.Remaining() {
				reservedEstimate = l.tokens.Remaining()
			}
			if reservedEstimate > 0 {
				if !l.tokens.Reserve(reservedEstimate) {
					bs := l.tokens.State()
					telemetry.Warnf("budget reserve failed: want=%d remaining=%d used=%d", reservedEstimate, bs.Remaining, bs.Used)
					reservedEstimate = 0
				} else if l.tokens.IsDeterministic() {
					telemetry.Warnf("deterministic mode: budget reserved=%d step=%d", reservedEstimate, step)
				}
			}
		}

		outcomes, p, needBreak := l.runToolCalls(ctx, session, step, calls, skillForTools, opts.AutoApprove, publish, auditLog)
		totalTools += len(outcomes)

		// Commit or release reserved budget after tool execution
		if l.tokens != nil && reservedEstimate > 0 {
			actualTokens := 0
			for _, o := range outcomes {
				actualTokens += common.EstimateTokens(o.text)
			}
			if actualTokens > reservedEstimate {
				actualTokens = reservedEstimate
			}
			l.tokens.Commit(actualTokens)
			l.tokens.Release(reservedEstimate - actualTokens)
		}

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
		// Emit incremental plan progress for UI rendering.
		if taskPlan != nil {
			publishPlanUpdate(taskPlan, "step")
			// Auto re-plan when tool failures pile up (interruptible: the new
			// plan is proposed via EventReplan and the loop continues).
			if streak >= replanFailStreak {
				replanPending = true
			}
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
	if l.tokens != nil {
		bs := l.tokens.State()
		telemetry.TraceEvent(map[string]any{
			"event": "budget_state", "session": session.ID,
			"total": bs.Total, "spent": bs.Spent, "reserved": bs.Reserved,
			"used": bs.Used, "remaining": bs.Remaining,
		})
	}
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
