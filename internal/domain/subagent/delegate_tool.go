package subagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// DelegateTool launches one or more subagents (lives in subagent package to avoid import cycles).
type DelegateTool struct {
	Runner *Runner
}

func NewDelegateTool(r *Runner) *DelegateTool {
	return &DelegateTool{Runner: r}
}

func (t *DelegateTool) Name() string { return "delegate" }
func (t *DelegateTool) Description() string {
	return `Delegate work to SubAgent(s). Args:
- prompt: single task (string)
- role: explore|verify|general (optional)
- isolation: worktree (optional)
- maxSteps: int (optional)
- tasks: array of {prompt, role?, tools?, isolation?, id?} for parallel run`
}

func (t *DelegateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt":    map[string]any{"type": "string"},
			"role":      map[string]any{"type": "string"},
			"isolation": map[string]any{"type": "string"},
			"maxSteps":  map[string]any{"type": "integer"},
			"tasks":     map[string]any{"type": "array"},
		},
	}
}

func (t *DelegateTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	if t.Runner == nil {
		return tool.Result{Text: "subagent runner unavailable", IsError: true}, nil
	}
	specs := parseSpecs(args)
	if len(specs) == 0 {
		return tool.Result{Text: "delegate requires prompt or tasks[]", IsError: true}, nil
	}
	results := t.Runner.RunAll(ctx, specs)
	return tool.Result{Text: FormatResults(results)}, nil
}

func parseSpecs(args map[string]any) []Spec {
	var specs []Spec
	if raw, ok := args["tasks"].([]any); ok && len(raw) > 0 {
		for i, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			sp := Spec{
				ID: str(m["id"]), Prompt: str(m["prompt"]), Role: str(m["role"]),
				Isolation: str(m["isolation"]),
			}
			if sp.ID == "" {
				sp.ID = fmt.Sprintf("task-%d", i+1)
			}
			if v, ok := m["maxSteps"].(float64); ok {
				sp.MaxSteps = int(v)
			}
			if tools, ok := m["tools"].([]any); ok {
				for _, t := range tools {
					if s, ok := t.(string); ok {
						sp.Tools = append(sp.Tools, s)
					}
				}
			}
			if sp.Prompt != "" {
				specs = append(specs, sp)
			}
		}
		return specs
	}
	prompt := str(args["prompt"])
	if prompt == "" {
		return nil
	}
	sp := Spec{ID: "task-1", Prompt: prompt, Role: str(args["role"]), Isolation: str(args["isolation"])}
	if v, ok := args["maxSteps"].(float64); ok {
		sp.MaxSteps = int(v)
	}
	return []Spec{sp}
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
