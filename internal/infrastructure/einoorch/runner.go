package einoorch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
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
	APIBase    string // optional OpenAI-compatible base URL
	Model      string
	MaxSteps   int
	ByAzure    bool
	APIVersion string
}

// Runner uses CloudWeGo Eino ReAct agent for the think-act-observe loop.
// Domain tools + Guard are preserved; only the orchestration graph is Eino's.
type Runner struct {
	cfg      Config
	tools    *tool.MapRegistry
	perm     *security.Guard
	sessions sessrepo.ISessionRepository
	messages sessrepo.IMessageRepository
	persona  string
}

// NewRunner builds an Eino orchestration runner.
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
	return &Runner{
		cfg: cfg, tools: tools, perm: perm,
		sessions: sessions, messages: messages,
		persona: defaultPersona(),
	}
}

func (r *Runner) Permission() *security.Guard { return r.perm }

// Run implements engine.Runner using Eino react.Agent.
func (r *Runner) Run(ctx context.Context, session *sessmodel.Session, userInput string, eventCh chan<- *engine.Event, opts engine.RunOptions) (*engine.Result, error) {
	publish := func(ev *engine.Event) {
		if eventCh == nil || ev == nil {
			return
		}
		select {
		case eventCh <- ev:
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return nil, fmt.Errorf("empty input")
	}

	publish(&engine.Event{Type: engine.EventThought, Content: "Eino ReAct orchestrator start", Timestamp: time.Now().UnixMilli()})

	// persist user
	if r.messages != nil {
		_ = r.messages.Save(ctx, sessmodel.NewMessage(
			fmt.Sprintf("msg-%d", time.Now().UnixNano()), session.ID, "user", userInput,
		))
	}

	cm, err := r.newChatModel(ctx)
	if err != nil {
		publish(&engine.Event{Type: engine.EventError, Content: err.Error(), Completed: true, Timestamp: time.Now().UnixMilli()})
		return &engine.Result{SessionID: session.ID, Response: "LLM init error: " + err.Error(), ErrorClass: "llm"}, nil
	}

	einoTools := WrapRegistry(r.tools, r.perm)
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: einoTools,
		},
		MaxStep: r.cfg.MaxSteps,
		MessageModifier: react.NewPersonaModifier(r.persona),
	})
	if err != nil {
		return &engine.Result{SessionID: session.ID, Response: "eino agent: " + err.Error(), ErrorClass: "eino"}, nil
	}

	// permission context for tools
	tctx := WithSession(ctx, session.ID, opts.AutoApprove)

	// history → messages
	msgs := []*schema.Message{schema.UserMessage(userInput)}
	if r.messages != nil {
		if hist, err := r.messages.ListAsMaps(ctx, session.ID, 20); err == nil {
			// rebuild excluding the message we just saved (last user)
			built := mapsToSchema(hist)
			if len(built) > 0 {
				// drop trailing duplicate user if same
				msgs = append(built[:len(built)-1], schema.UserMessage(userInput))
			}
		}
	}

	publish(&engine.Event{Type: engine.EventThought, Content: fmt.Sprintf("eino tools=%d maxStep=%d", len(einoTools), r.cfg.MaxSteps), Timestamp: time.Now().UnixMilli()})

	out, err := agent.Generate(tctx, msgs)
	if err != nil {
		publish(&engine.Event{Type: engine.EventError, Content: err.Error(), Completed: true, Timestamp: time.Now().UnixMilli()})
		return &engine.Result{SessionID: session.ID, Response: "eino generate: " + err.Error(), ErrorClass: "eino"}, nil
	}

	final := ""
	if out != nil {
		final = strings.TrimSpace(out.Content)
	}
	if final == "" {
		final = "done."
	}

	// detect confirm pending
	var pending *security.PendingConfirm
	needPerm := false
	if r.perm != nil {
		list := r.perm.ListPending(session.ID)
		if len(list) > 0 {
			pending = list[0]
			needPerm = true
			if !strings.Contains(final, "CONFIRM") {
				final = fmt.Sprintf("CONFIRM required id=%s tool=%s\n%s", pending.ID, pending.Tool, final)
			}
		}
	}

	if r.messages != nil {
		_ = r.messages.Save(ctx, sessmodel.NewMessage(
			fmt.Sprintf("msg-%d", time.Now().UnixNano()), session.ID, "assistant", final,
		))
	}
	if r.sessions != nil {
		session.AddTokens(common.EstimateTokens(userInput) + common.EstimateTokens(final))
		_ = r.sessions.Save(ctx, session)
	}

	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: time.Now().UnixMilli()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino",
		"toolCalls":    0,
	}, Timestamp: time.Now().UnixMilli()})

	observability.Infof("eino run session=%s len=%d needPerm=%v", session.ID, len(final), needPerm)

	res := &engine.Result{
		SessionID: session.ID, Response: final, Steps: 1,
		TokenUsed: common.EstimateTokens(userInput) + common.EstimateTokens(final),
		NeedPermission: needPerm, Pending: pending,
	}
	if needPerm {
		res.ErrorClass = "permission"
	}
	return res, nil
}

func (r *Runner) newChatModel(ctx context.Context) (*openai.ChatModel, error) {
	cfg := &openai.ChatModelConfig{
		APIKey: r.cfg.APIKey,
		Model:  r.cfg.Model,
	}
	if r.cfg.APIBase != "" {
		// OpenAI-compatible: many providers use BaseURL
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
		case "tool":
			// skip raw tool rows for now (Eino expects tool call linkage)
		}
	}
	return out
}

func defaultPersona() string {
	return `You are Code-Agent, a coding agent running on Eino ReAct orchestration.
You work in a sandboxed workspace. Use tools for file/shell operations.
Prefer edit_file over full rewrite. Be concise.
If a tool returns DENIED or CONFIRM, stop and explain to the user what to approve.
Dangerous operations require human confirmation (handled by the tool guard).`
}

// Ensure interface compliance.
var _ engine.Runner = (*Runner)(nil)
