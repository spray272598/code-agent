package einoorch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

// MultiAgent runs lightweight parallel ReAct agents (explore + verify style).
// Complements domain SubAgent; uses same Guarded tools.
type MultiAgent struct {
	parent *Runner
}

func NewMultiAgent(parent *Runner) *MultiAgent {
	return &MultiAgent{parent: parent}
}

type multiResult struct {
	Role   string
	Output string
	Err    string
}

// RunParallel launches explore + verify (or general) Eino agents concurrently.
func (m *MultiAgent) RunParallel(
	ctx context.Context,
	session *sessmodel.Session,
	userInput string,
	publish EventSink,
	opts engine.RunOptions,
) (*engine.Result, error) {
	if m == nil || m.parent == nil {
		return nil, fmt.Errorf("multi-agent unavailable")
	}
	goal := strings.TrimSpace(userInput)
	for _, p := range []string{"/team", "/parallel", "team mode:", "parallel explore:"} {
		if strings.HasPrefix(strings.ToLower(goal), p) {
			goal = strings.TrimSpace(goal[len(p):])
		}
	}
	if goal == "" {
		goal = userInput
	}

	publish(&engine.Event{
		Type: engine.EventSubAgent, SubType: "start",
		Content: "Eino multi-agent: explore + verify", Timestamp: nowMs(),
	})

	roles := []struct {
		role    string
		prompt  string
		tools   []string // empty = all
	}{
		{"explore", "Investigate and gather facts (read-only preferred):\n" + goal, []string{"read_file", "glob", "grep", "memory_search"}},
		{"verify", "Verify findings, list risks and checks:\n" + goal, []string{"read_file", "grep", "glob", "bash"}},
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		outs []multiResult
	)
	for _, r := range roles {
		wg.Add(1)
		go func(role, prompt string, allow []string) {
			defer wg.Done()
			publish(&engine.Event{
				Type: engine.EventSubAgent, SubType: role,
				Content: "start " + role, Timestamp: nowMs(),
			})
			text, err := m.runOne(ctx, session.ID, prompt, allow, opts.AutoApprove, publish)
			mr := multiResult{Role: role, Output: text}
			if err != nil {
				mr.Err = err.Error()
				if mr.Output == "" {
					mr.Output = err.Error()
				}
			}
			mu.Lock()
			outs = append(outs, mr)
			mu.Unlock()
			publish(&engine.Event{
				Type: engine.EventSubAgent, SubType: role,
				Content: "done " + role + ": " + truncate(text, 120), Timestamp: nowMs(),
			})
		}(r.role, r.prompt, r.tools)
	}
	wg.Wait()

	// merge step (serial general agent)
	var mergeIn strings.Builder
	mergeIn.WriteString("Merge the following agent reports into one actionable answer for the user.\nGoal: " + goal + "\n\n")
	for _, o := range outs {
		mergeIn.WriteString("### " + o.Role + "\n" + o.Output + "\n\n")
	}
	final, err := m.runOne(ctx, session.ID, mergeIn.String(), nil, opts.AutoApprove, publish)
	if err != nil && final == "" {
		final = "merge error: " + err.Error()
	}
	if final == "" {
		// fallback concatenate
		var b strings.Builder
		for _, o := range outs {
			b.WriteString("## " + o.Role + "\n" + o.Output + "\n")
		}
		final = b.String()
	}

	m.parent.persistAssistant(ctx, session, final)
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino-multi", "agents": len(outs),
	}, Timestamp: nowMs()})

	return &engine.Result{
		SessionID: session.ID, Response: final, Steps: len(outs) + 1,
		ToolCalls: len(outs), TokenUsed: common.EstimateTokens(final),
	}, nil
}

func (m *MultiAgent) runOne(ctx context.Context, sessionID, prompt string, allow []string, auto bool, publish EventSink) (string, error) {
	cm, err := m.parent.newChatModel(ctx)
	if err != nil {
		return "", err
	}
	cross := m.parent.crossCut("")
	var tools []einotool.BaseTool
	if len(allow) == 0 {
		tools = WrapRegistryCross(m.parent.tools, m.parent.perm, cross)
	} else {
		reg := domtool.NewRegistry()
		for _, name := range allow {
			if t := m.parent.tools.Get(name); t != nil {
				reg.Register(t)
			}
		}
		tools = WrapRegistryCross(reg, m.parent.perm, cross)
	}
	// cap steps for sub agents
	maxStep := 6
	if m.parent.cfg.MaxSteps > 0 && m.parent.cfg.MaxSteps < maxStep {
		maxStep = m.parent.cfg.MaxSteps
	}
	ag, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		MaxStep:          maxStep,
		MessageModifier: react.NewPersonaModifier(
			"You are a focused sub-agent. Use only necessary tools. Be concise. Goal-oriented."),
	})
	if err != nil {
		return "", err
	}
	tctx := WithSession(ctx, sessionID, auto)
	stats := &runStats{}
	opt := agentOptions(publish, stats)
	// short timeout per subagent
	cctx, cancel := context.WithTimeout(tctx, 90*time.Second)
	defer cancel()
	out, err := ag.Generate(cctx, []*schema.Message{schema.UserMessage(prompt)}, opt)
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", nil
	}
	return strings.TrimSpace(out.Content), nil
}
