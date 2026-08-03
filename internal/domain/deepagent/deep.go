// Package deepagent implements a sequential Plan→Act→Reflect orchestration
// contrasting with role-parallel Teams (explore||verify → merge).
package deepagent

import (
	"fmt"
	"strings"
)

// Mode identifiers.
const (
	ModeDeep  = "deep"  // sequential deep agent
	ModeTeams = "teams" // parallel roles (see domain/team)
)

// Phase is one step of DeepAgent.
type Phase struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Prompt   string   `json:"prompt"`
	Tools    []string `json:"tools,omitempty"` // empty = all
	MaxSteps int      `json:"maxSteps,omitempty"`
}

// Expand builds Plan → Act → Reflect phases for a user goal.
func Expand(goal string) []Phase {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "complete the user task"
	}
	return []Phase{
		{
			ID: "plan", Name: "Plan",
			Prompt: "You are the PLANNER of a DeepAgent. Break the goal into 3-6 concrete steps. " +
				"List files to inspect and risks. Do NOT implement yet.\nGoal:\n" + goal,
			Tools:    []string{"code_search", "glob", "grep", "read_file", "memory_search"},
			MaxSteps: 5,
		},
		{
			ID: "act", Name: "Act",
			Prompt: "You are the EXECUTOR of a DeepAgent. Follow the plan and implement. " +
				"Use tools to read/edit/run tests. Prefer minimal diffs.\nGoal:\n" + goal +
				"\n(Use prior planner notes from conversation if present.)",
			Tools:    []string{"code_search", "read_file", "write_file", "edit_file", "bash", "glob", "grep", "code_index"},
			MaxSteps: 12,
		},
		{
			ID: "reflect", Name: "Reflect",
			Prompt: "You are the REVIEWER of a DeepAgent. Critique the work: gaps, bugs, missing tests. " +
				"If solid, summarize final answer for the user. If not, list fix steps.\nGoal:\n" + goal,
			Tools:    []string{"code_search", "read_file", "grep", "glob", "bash"},
			MaxSteps: 6,
		},
	}
}

// ComparisonDoc is a short interview-ready contrast table (markdown).
func ComparisonDoc() string {
	return strings.TrimSpace(`
# DeepAgent vs Teams

| Dimension | DeepAgent (` + "`/deep`" + `) | Teams (` + "`/team`" + ` / parallel) |
|-----------|-------------------------------|--------------------------------------|
| Topology | **Sequential** Plan → Act → Reflect | **Parallel** explore ‖ verify → merge |
| Strength | Depth, consistency, fewer race edits | Breadth, multi-angle coverage |
| Weakness | Higher latency (serial phases) | Merge quality depends on merge step |
| Tools | Phase-scoped allowlists | Role-scoped allowlists (teams/default.yaml) |
| Isolation | Same session context chain | Subagents / optional worktree |
| Best for | Single feature end-to-end | Investigation + verification fan-out |
| Resume | Checkpoint per session (HITL) | Each subagent independent |

**Rule of thumb**: use DeepAgent when one coherent change must land; use Teams when you need concurrent exploration and critique.
`) + "\n"
}

// PhaseOut is a completed phase result.
type PhaseOut struct {
	ID, Name, Output string
}

// FormatPhaseResults combines phase outputs for parent observation.
func FormatPhaseResults(goal string, parts []PhaseOut) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## DeepAgent result (goal)\n%s\n\n", goal))
	for _, p := range parts {
		b.WriteString(fmt.Sprintf("### [%s] %s\n%s\n\n", p.ID, p.Name, p.Output))
	}
	return b.String()
}

// LooksDeep detects deep-agent routing prefixes.
func LooksDeep(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"/deep", "deepagent:", "deep agent:", "mode:deep"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// StripPrefix removes deep routing prefix from user input.
func StripPrefix(s string) string {
	raw := strings.TrimSpace(s)
	low := strings.ToLower(raw)
	for _, p := range []string{"/deep", "deepagent:", "deep agent:", "mode:deep"} {
		if strings.HasPrefix(low, p) {
			return strings.TrimSpace(raw[len(p):])
		}
	}
	return raw
}
