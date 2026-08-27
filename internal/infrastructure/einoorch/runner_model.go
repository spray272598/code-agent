package einoorch

// runner_model.go — OpenAI chat model instantiation, per-intent model routing, and tool filtering.

import (
	"context"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	einotool "github.com/cloudwego/eino/components/tool"
)

// applyRoute selects the model endpoint for the given intent and updates the
// active config so subsequent newChatModel calls use it. No-op when no Router
// is configured (single-model behavior preserved).
func (r *Runner) applyRoute(intent string) {
	if r.cfg.Router == nil {
		return
	}
	route := r.cfg.Router.Select(intent)
	if route.Model != "" {
		r.cfg.Model = route.Model
	}
	if route.APIBase != "" {
		r.cfg.APIBase = route.APIBase
	}
	if route.APIKey != "" {
		r.cfg.APIKey = route.APIKey
	}
}

// newChatModel creates a new OpenAI ChatModel from the runner's current config.
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

// filterEinoToolsByAllow keeps only tools whose names appear in the allow list.
// Returns the full list when allow is empty or contains "*".
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
