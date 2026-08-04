package einoorch

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// agentHandle wraps either stock react.Agent or a recompiled Runnable with CheckPointStore.
type agentHandle struct {
	agent *react.Agent
	// run is non-nil when graph was recompiled with CheckPointStore for in-graph resume.
	run compose.Runnable[[]*schema.Message, *schema.Message]
}

func (h *agentHandle) Generate(ctx context.Context, msgs []*schema.Message, opts ...agent.AgentOption) (*schema.Message, error) {
	if h == nil {
		return nil, fmt.Errorf("nil agent")
	}
	if h.run != nil {
		return h.run.Invoke(ctx, msgs, agent.GetComposeOptions(opts...)...)
	}
	if h.agent == nil {
		return nil, fmt.Errorf("nil agent")
	}
	return h.agent.Generate(ctx, msgs, opts...)
}

func (h *agentHandle) Stream(ctx context.Context, msgs []*schema.Message, opts ...agent.AgentOption) (*schema.StreamReader[*schema.Message], error) {
	if h == nil {
		return nil, fmt.Errorf("nil agent")
	}
	if h.run != nil {
		return h.run.Stream(ctx, msgs, agent.GetComposeOptions(opts...)...)
	}
	if h.agent == nil {
		return nil, fmt.Errorf("nil agent")
	}
	return h.agent.Stream(ctx, msgs, opts...)
}

// buildReactAgent creates a ReAct agent; when store != nil, recompiles the graph
// with CheckPointStore so StatefulInterrupt can persist + Resume.
func buildReactAgent(ctx context.Context, cfg *react.AgentConfig, store compose.CheckPointStore) (*agentHandle, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil agent config")
	}
	ag, err := react.NewAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}
	h := &agentHandle{agent: ag}
	if store == nil {
		return h, nil
	}
	anyG, _ := ag.ExportGraph()
	g, ok := anyG.(*compose.Graph[[]*schema.Message, *schema.Message])
	if !ok || g == nil {
		// fall back to stock agent without graph resume
		return h, nil
	}
	name := cfg.GraphName
	if name == "" {
		name = "ReActAgent"
	}
	maxStep := cfg.MaxStep
	if maxStep <= 0 {
		maxStep = 20
	}
	run, err := g.Compile(ctx,
		compose.WithMaxRunSteps(maxStep),
		compose.WithNodeTriggerMode(compose.AnyPredecessor),
		compose.WithGraphName(name),
		compose.WithCheckPointStore(store),
	)
	if err != nil {
		// keep stock agent if recompile fails
		return h, nil
	}
	h.run = run
	return h, nil
}

// graphResumeOpts builds compose options for a checkpointed generate/stream.
func graphResumeOpts(checkPointID string, forceNew bool) []agent.AgentOption {
	var cos []compose.Option
	if checkPointID != "" {
		cos = append(cos, compose.WithCheckPointID(checkPointID))
	}
	if forceNew {
		cos = append(cos, compose.WithForceNewRun())
	}
	if len(cos) == 0 {
		return nil
	}
	return []agent.AgentOption{agent.WithComposeOptions(cos...)}
}
