package team

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"gopkg.in/yaml.v3"
)

const (
	ModeParallel = "parallel"
	ModeReview   = "review"
	ModeDebate   = "debate"
	ModeMerge    = "merge"
)

var ValidModes = []string{ModeParallel, ModeReview, ModeDebate, ModeMerge}

type Config struct {
	Name  string                `yaml:"name"`
	Mode  string                `yaml:"mode"`
	Roles map[string]RoleConfig `yaml:"roles"`
}

type RoleConfig struct {
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
	MaxSteps    int      `yaml:"max_steps"`
}

func LoadYAML(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func ApplyToRunner(r *subagent.Runner, c *Config) {
	if r == nil || c == nil || len(c.Roles) == 0 {
		return
	}
	if r.Roles == nil {
		r.Roles = map[string]subagent.RoleConfig{}
	}
	for name, rc := range c.Roles {
		key := strings.ToLower(name)
		r.Roles[key] = subagent.RoleConfig{
			Tools:    append([]string{}, rc.Tools...),
			MaxSteps: rc.MaxSteps,
		}
	}
}

func ListRoles(c *Config) string {
	if c == nil || len(c.Roles) == 0 {
		return "default roles: explore, verify, general"
	}
	var b strings.Builder
	b.WriteString("team: ")
	b.WriteString(c.Name)
	if c.Mode != "" {
		b.WriteString(" mode=")
		b.WriteString(c.Mode)
	}
	b.WriteString("\n")
	for name, rc := range c.Roles {
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(rc.Description)
		b.WriteString(" tools=")
		b.WriteString(strings.Join(rc.Tools, ","))
		b.WriteString("\n")
	}
	return b.String()
}

func ExpandCollaboration(mode, goal string) []subagent.Spec {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeParallel
	}
	switch mode {
	case ModeReview:
		return []subagent.Spec{
			{ID: "review-explore", Role: "explore", Prompt: "Investigate: " + goal, Isolation: "worktree"},
			{ID: "review-verify", Role: "verify", Prompt: "Review findings and verify for: " + goal +
				". Critique gaps and list checks.", Isolation: "process"},
		}
	case ModeDebate:
		return []subagent.Spec{
			{ID: "debate-a", Role: "explore", Prompt: "Argue approach A for: " + goal},
			{ID: "debate-b", Role: "verify", Prompt: "Argue approach B (alternative) for: " + goal},
			{ID: "debate-merge", Role: "general", Prompt: "Merge debate into one recommendation for: " + goal},
		}
	case ModeMerge:
		return []subagent.Spec{
			{ID: "merge-explore", Role: "explore", Prompt: "Collect facts for: " + goal},
			{ID: "merge-verify", Role: "verify", Prompt: "Collect verification notes for: " + goal},
			{ID: "merge-final", Role: "general", Prompt: "Merge all prior worker notes into a single plan for: " + goal},
		}
	default:
		return []subagent.Spec{
			{ID: "p-explore", Role: "explore", Prompt: goal, Isolation: "worktree"},
			{ID: "p-verify", Role: "verify", Prompt: "Verify aspects of: " + goal, Isolation: "process"},
		}
	}
}

func FormatResults(mode string, results []subagent.Result) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Team mode=%s (%d agents)\n", mode, len(results)))
	for _, r := range results {
		b.WriteString(fmt.Sprintf("### [%s] role=%s status=%s\n%s\n\n", r.ID, r.Role, r.Status, r.Output))
	}
	return b.String()
}

// NewEngine creates a collaboration engine from a team config.
func NewEngine(cfg *Config, runner *subagent.Runner, llm port.ILLMPort) *Engine {
	return &Engine{cfg: cfg, runner: runner, llm: llm}
}

// Engine is the entry point for team-based collaboration.
type Engine struct {
	cfg    *Config
	runner *subagent.Runner
	llm    port.ILLMPort
}

// Run starts a collaboration workflow using the team config's mode.
func (e *Engine) Run(ctx context.Context, goal string) (*CollaborationState, error) {
	mode := ModeParallel
	if e.cfg != nil && e.cfg.Mode != "" {
		mode = strings.ToLower(e.cfg.Mode)
	}
	collab := NewCollaboration(mode, goal, e.runner, e.llm)
	return collab.Run(ctx)
}

// CreateCollaboration creates a Collaboration for manual orchestration.
func (e *Engine) CreateCollaboration(goal string) *Collaboration {
	mode := ModeParallel
	if e.cfg != nil && e.cfg.Mode != "" {
		mode = strings.ToLower(e.cfg.Mode)
	}
	return NewCollaboration(mode, goal, e.runner, e.llm)
}

// ModeDescriptions returns human-readable descriptions of each mode.
func ModeDescriptions() map[string]string {
	return map[string]string{
		ModeParallel: "Parallel: multiple agents work simultaneously, results are merged",
		ModeReview:   "Review: explore → verify → feedback → fix loop with up to 2 rounds",
		ModeDebate:   "Debate: two agents argue different approaches, third synthesizes",
		ModeMerge:    "Merge: workers collect facts, then a synthesizer merges into one answer",
	}
}

// ValidateMode checks if a mode string is valid.
func ValidateMode(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	for _, m := range ValidModes {
		if mode == m {
			return true
		}
	}
	return false
}
