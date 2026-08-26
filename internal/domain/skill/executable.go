package skill

import (
	"context"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ExecutableSkill wraps a Skill so it can be invoked like a tool by the
// Agent loop. This implements the "Skill可执行" requirement: a skill can
// be called directly with arguments and returns the substituted body as
// the tool result.
type ExecutableSkill struct {
	skill *Skill
	svc   *Service
}

// NewExecutableSkill wraps a skill for direct execution.
func (s *Service) NewExecutableSkill(sk *Skill) *ExecutableSkill {
	return &ExecutableSkill{skill: sk, svc: s}
}

func (e *ExecutableSkill) Name() string {
	return "skill_" + e.skill.ID
}

func (e *ExecutableSkill) Description() string {
	return "[skill] " + e.skill.Description + " (invoke skill " + e.skill.Name + ")"
}

func (e *ExecutableSkill) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"args": map[string]any{"type": "string", "description": "Arguments passed to the skill"}},
	}
}

func (e *ExecutableSkill) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	argsStr := ""
	if a, ok := args["args"].(string); ok {
		argsStr = a
	}

	// Compose with dependencies
	composed := e.svc.Compose(e.skill)
	var b strings.Builder
	for _, c := range composed {
		if c.Body == "" {
			continue
		}
		substituted := Substitute(c.Body, argsStr, SubstitutionContext{
			SkillDir:  c.Path,
			SessionID: sessionIDFromCtx(ctx),
		})
		b.WriteString("<skill name=\"")
		b.WriteString(c.Name)
		b.WriteString("\">\n")
		b.WriteString(substituted)
		b.WriteString("\n</skill>\n\n")
	}
	return tool.Result{Text: b.String()}, nil
}

// sessionIDFromCtx is a best-effort lookup for a session ID in context.
// Returns empty when not set. This keeps the skill system independent of
// the session package; callers can inject the ID via context value.
func sessionIDFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	type sidKey struct{}
	if v, ok := ctx.Value(sidKey{}).(string); ok {
		return v
	}
	return ""
}

// WithSessionID returns a derived context carrying the session ID.
func WithSessionID(ctx context.Context, id string) context.Context {
	type sidKey struct{}
	return context.WithValue(ctx, sidKey{}, id)
}
