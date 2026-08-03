package team

import (
	"fmt"
	"os"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/subagent"
	"gopkg.in/yaml.v3"
)

// Collaboration modes (Claude Code / multi-agent patterns).
const (
	ModeParallel = "parallel" // independent roles run together (default)
	ModeReview   = "review"   // explore → verify critiques output
	ModeDebate   = "debate"   // two roles argue, general merges
	ModeMerge    = "merge"    // collect outputs then merge-only step
)

// Config maps role names to tool allowlists (thin Agent Teams).
type Config struct {
	Name  string                `yaml:"name"`
	Mode  string                `yaml:"mode"` // parallel|review|debate|merge
	Roles map[string]RoleConfig `yaml:"roles"`
}

type RoleConfig struct {
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
	MaxSteps    int      `yaml:"max_steps"`
}

// LoadYAML file; on failure returns nil.
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

// ApplyToRunner merges team roles into subagent runner.
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
			Tools: append([]string{}, rc.Tools...),
			MaxSteps: rc.MaxSteps,
		}
	}
}

// ListRoles for slash/help.
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

// ExpandCollaboration turns a user goal + mode into ordered/parallel subagent specs.
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
	default: // parallel
		return []subagent.Spec{
			{ID: "p-explore", Role: "explore", Prompt: goal, Isolation: "worktree"},
			{ID: "p-verify", Role: "verify", Prompt: "Verify aspects of: " + goal, Isolation: "process"},
		}
	}
}

// FormatResults combines multi-agent outputs for parent Observation.
func FormatResults(mode string, results []subagent.Result) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Team mode=%s (%d agents)\n", mode, len(results)))
	for _, r := range results {
		b.WriteString(fmt.Sprintf("### [%s] role=%s status=%s\n%s\n\n", r.ID, r.Role, r.Status, r.Output))
	}
	return b.String()
}
