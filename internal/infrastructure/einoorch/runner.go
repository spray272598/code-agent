package einoorch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/types/common"
)

// Config for Eino-backed orchestration.
type Config struct {
	APIKey     string
	APIBase    string
	Model      string
	MaxSteps   int
	ByAzure    bool
	APIVersion string
	// UseStream enables Stream path with text_delta events (default false for stability)
	UseStream bool
}

// Runner uses CloudWeGo Eino ReAct agent for the think-act-observe loop.
type Runner struct {
	cfg      Config
	tools    *tool.MapRegistry
	perm     *security.Guard
	sessions sessrepo.ISessionRepository
	messages sessrepo.IMessageRepository
	persona  string
	// Multi is optional multi-agent helper (explore|verify parallel)
	Multi *MultiAgent
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
	r := &Runner{
		cfg: cfg, tools: tools, perm: perm,
		sessions: sessions, messages: messages,
		persona: defaultPersona(),
	}
	r.Multi = NewMultiAgent(r)
	return r
}

func (r *Runner) Permission() *security.Guard { return r.perm }

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

	publish(&engine.Event{Type: engine.EventThought, Content: "Eino ReAct orchestrator", Timestamp: nowMs()})

	// resume approved tool before continuing agent (align with native Loop)
	if r.perm != nil && isContinue(userInput) {
		if ready := r.perm.TakeReadyResume(session.ID); ready != nil {
			publish(&engine.Event{Type: engine.EventResume, SubType: ready.Tool, Content: "resume " + ready.Tool, Timestamp: nowMs()})
			resText := r.execDomainTool(ctx, ready.Tool, ready.Args)
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
				"\nContinue the task with Thought/tools or final answer."
		}
	}

	if r.messages != nil {
		_ = r.messages.Save(ctx, sessmodel.NewMessage(idMsg(), session.ID, "user", userInput))
	}

	// multi-agent shortcut: prefix /team or "parallel explore"
	if r.Multi != nil && looksMulti(userInput) {
		return r.Multi.RunParallel(ctx, session, userInput, publish, opts)
	}

	cm, err := r.newChatModel(ctx)
	if err != nil {
		publish(&engine.Event{Type: engine.EventError, Content: err.Error(), Completed: true, Timestamp: nowMs()})
		return &engine.Result{SessionID: session.ID, Response: "LLM init error: " + err.Error(), ErrorClass: "llm"}, nil
	}

	einoTools := WrapRegistry(r.tools, r.perm)
	agentInst, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: einoTools,
		},
		MaxStep:         r.cfg.MaxSteps,
		MessageModifier: react.NewPersonaModifier(r.persona),
	})
	if err != nil {
		return &engine.Result{SessionID: session.ID, Response: "eino agent: " + err.Error(), ErrorClass: "eino"}, nil
	}

	tctx := WithSession(ctx, session.ID, opts.AutoApprove)
	tctx = WithEventSink(tctx, publish)

	msgs := r.buildMessages(ctx, session.ID, userInput)
	publish(&engine.Event{
		Type: engine.EventThought,
		Content: fmt.Sprintf("eino tools=%d maxStep=%d stream=%v", len(einoTools), r.cfg.MaxSteps, r.cfg.UseStream),
		Timestamp: nowMs(),
	})

	cbOpt := agentOptions(publish, stats)
	var final string
	var genErr error

	if r.cfg.UseStream {
		final, genErr = r.runStream(tctx, agentInst, msgs, publish, cbOpt)
	} else {
		out, err := agentInst.Generate(tctx, msgs, cbOpt)
		genErr = err
		if out != nil {
			final = strings.TrimSpace(out.Content)
		}
	}

	// Interrupt / HITL
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
	if r.sessions != nil {
		session.AddTokens(common.EstimateTokens(userInput) + common.EstimateTokens(final))
		_ = r.sessions.Save(ctx, session)
	}

	tc := int(stats.toolCalls.Load())
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino", "toolCalls": tc, "toolErrors": stats.toolErrs.Load(),
	}, Timestamp: nowMs()})

	observability.Infof("eino run session=%s tools=%d needPerm=%v", session.ID, tc, needPerm)
	res := &engine.Result{
		SessionID: session.ID, Response: final, Steps: tc + 1, ToolCalls: tc,
		TokenUsed: common.EstimateTokens(userInput) + common.EstimateTokens(final),
		NeedPermission: needPerm, Pending: pending,
	}
	if needPerm {
		res.ErrorClass = "permission"
	}
	return res, nil
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
			// EOF
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

func (r *Runner) buildMessages(ctx context.Context, sessionID, userInput string) []*schema.Message {
	msgs := []*schema.Message{schema.UserMessage(userInput)}
	if r.messages == nil {
		return msgs
	}
	hist, err := r.messages.ListAsMaps(ctx, sessionID, 24)
	if err != nil || len(hist) == 0 {
		return msgs
	}
	built := mapsToSchema(hist)
	if len(built) == 0 {
		return msgs
	}
	// replace trailing user with current input
	return append(built[:len(built)-1], schema.UserMessage(userInput))
}

func (r *Runner) execDomainTool(ctx context.Context, name string, args map[string]any) string {
	if r.tools == nil {
		return "tool registry unavailable"
	}
	t := r.tools.Get(name)
	if t == nil {
		return "tool not found: " + name
	}
	res, err := t.Execute(ctx, args)
	if err != nil {
		return err.Error()
	}
	return res.Text
}

func (r *Runner) persistAssistant(ctx context.Context, session *sessmodel.Session, text string) {
	if r.messages == nil {
		return
	}
	_ = r.messages.Save(ctx, sessmodel.NewMessage(idMsg(), session.ID, "assistant", text))
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

func mapsToSchema(hist []map[string]any) []*schema.Message {
	out := make([]*schema.Message, 0, len(hist))
	for _, m := range hist {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if content == "" {
			continue
		}
		switch role {
		case "user":
			out = append(out, schema.UserMessage(content))
		case "assistant":
			out = append(out, schema.AssistantMessage(content, nil))
		case "system":
			out = append(out, schema.SystemMessage(content))
		}
	}
	return out
}

func defaultPersona() string {
	return `You are Code-Agent on Eino ReAct orchestration.
Sandboxed workspace. Use tools for file/shell. Prefer edit_file.
If a tool returns DENIED or CONFIRM, explain what the user must approve.
For multi-step independent research, you may use the delegate tool when available.
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
	// Eino interrupt signal
	var target interface{ GetInterruptContexts() any }
	if errors.As(err, &target) {
		return true
	}
	// string heuristics
	msg := err.Error()
	return strings.Contains(msg, "interrupt") || strings.Contains(msg, "Interrupt")
}

func nowMs() int64 { return time.Now().UnixMilli() }

func idMsg() string {
	return fmt.Sprintf("msg-%d", time.Now().UnixNano())
}

var _ engine.Runner = (*Runner)(nil)
