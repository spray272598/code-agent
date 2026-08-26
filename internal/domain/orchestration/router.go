package orchestration

import (
	"regexp"
	"strings"
)

// OrchestratorMode identifies the topology best-suited for a user request (P2-2).
type OrchestratorMode int

const (
	ModeSingleAgent OrchestratorMode = iota
	ModeTeams
	ModeDeepAgent
)

// String returns a human-readable label.
func (m OrchestratorMode) String() string {
	switch m {
	case ModeTeams:
		return "teams"
	case ModeDeepAgent:
		return "deep"
	default:
		return "single"
	}
}

// Router picks the best agent topology for a user request.
// Heuristics:
//   - User explicitly requests /team, /parallel, /deep → honor it immediately.
//   - Multi-faceted requests (conjunctions, lists of tasks) → Teams.
//   - Requests with explicit step-by-step planning → DeepAgent.
//   - Everything else → SingleAgent (legacy path).
type Router struct {
	teamsPfx  []string
	deepPfx   []string
	teamRE    *regexp.Regexp
	deepRE    *regexp.Regexp
	parallelRE *regexp.Regexp
	planRE    *regexp.Regexp
	actionRE  *regexp.Regexp
}

// NewRouter returns a Router with sensible defaults.
func NewRouter() *Router {
	return &Router{
		teamsPfx:  []string{"/team", "/parallel", "/teams", "team mode", "parallel mode"},
		deepPfx:   []string{"/deep", "/deepagent", "deep mode"},
		teamRE:    regexp.MustCompile(`(?i)\b(compare|contrast|vs\.?|analyze (both|all|multiple)|multiple (files|modules|systems)|review (code|diff|report))\b`),
		deepRE:    regexp.MustCompile(`(?i)\b(plan|break ?down|step ?by ?step|implement|refactor|build (a|an|the)|design a)\b`),
		parallelRE: regexp.MustCompile(`(?i)\b(investigate|explore|survey|audit|search (for|the)|find (all|every))\b`),
		planRE:    regexp.MustCompile(`(?i)\b(plan|todo|task list|step 1|phase 1)\b`),
		actionRE:  regexp.MustCompile(`(?i)\b(how to|how do I|what (is|are)|explain|show me|list all|find out)\b`),
	}
}

// Decide returns the most appropriate topology given the user message.
// The `explicit` parameter lets callers override the decision (e.g. when user typed /team).
func (r *Router) Decide(input string, explicit OrchestratorMode) OrchestratorMode {
	if explicit != ModeSingleAgent {
		return explicit
	}
	return r.DecideAuto(input)
}

// DecideAuto picks a mode purely based on message content.
func (r *Router) DecideAuto(input string) OrchestratorMode {
	lower := strings.ToLower(strings.TrimSpace(input))

	// 1. Honor explicit prefixes (fast path).
	for _, p := range r.teamsPfx {
		if strings.HasPrefix(lower, p) {
			return ModeTeams
		}
	}
	for _, p := range r.deepPfx {
		if strings.HasPrefix(lower, p) {
			return ModeDeepAgent
		}
	}

	// 2. Multi-pattern heuristics (ordered by specificity).
	switch {
	case r.teamRE.MatchString(lower):
		return ModeTeams
	case r.deepRE.MatchString(lower):
		return ModeDeepAgent
	case r.planRE.MatchString(lower) && strings.Contains(lower, "implement"):
		return ModeDeepAgent
	}

	// 3. Multi-faceted enumeration: treat as Teams only if no strong deep signal.
	if r.hasMultipleTasks(lower) {
		return ModeTeams
	}

	// 4. Parallel-leaning verbs without "how to" / "explain" action patterns.
	if r.parallelRE.MatchString(lower) && !r.actionRE.MatchString(lower) {
		return ModeTeams
	}

	// 5. Default to single agent.
	return ModeSingleAgent
}

// hasMultipleTasks returns true when the message enumerates 2+ actions.
func (r *Router) hasMultipleTasks(s string) bool {
	separators := []string{",", " and ", ";", " then ", " followed by ", " then "}
	count := 0
	for _, sep := range separators {
		count += strings.Count(s, sep)
	}
	return count >= 2
}

// WithTeamsPrefix registers additional explicit team-mode prefixes.
func (r *Router) WithTeamsPrefix(prefixes ...string) *Router {
	r.teamsPfx = append(r.teamsPfx, prefixes...)
	return r
}

// WithDeepPrefix registers additional explicit deep-mode prefixes.
func (r *Router) WithDeepPrefix(prefixes ...string) *Router {
	r.deepPfx = append(r.deepPfx, prefixes...)
	return r
}

// Describe returns a human explanation of why a mode was chosen.
func (r *Router) Describe(input string, mode OrchestratorMode) string {
	switch mode {
	case ModeTeams:
		return "Selected Teams mode: the request appears to involve multiple independent findings (parallel agents)"
	case ModeDeepAgent:
		return "Selected DeepAgent mode: the request requires a step-by-step plan + reflect cycle"
	default:
		return "Selected SingleAgent mode: simple query handled by a single focused agent"
	}
}
