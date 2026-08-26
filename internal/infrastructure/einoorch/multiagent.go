package einoorch

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"gopkg.in/yaml.v3"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
	"github.com/spray272598/code-agent/internal/domain/orchestration"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/team"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

// MultiAgent runs lightweight parallel ReAct agents (explore + verify style).
// Complements domain SubAgent; uses same Guarded tools.
type MultiAgent struct {
	parent     *Runner
	teamCfg    *team.Config
	budgetMgr  *BudgetManager
	router     *orchestration.Router
	blackboard *orchestration.Blackboard
	journals   map[string]*orchestration.Journal
	journalCfg orchestration.JournalStorageConfig
	journalMu  sync.Mutex
}

// TeamConfigPath is the default YAML file for team role configuration.
const TeamConfigPath = "teams/default.yaml"

// LoadTeamConfig loads role configuration from YAML, with fallback to defaults.
func LoadTeamConfig(path string) *team.Config {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[multiagent] team config %s not found: %v; using defaults", path, err)
		return nil
	}
	var c team.Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		log.Printf("[multiagent] team config parse error: %v; using defaults", err)
		return nil
	}
	return &c
}

// NewMultiAgent creates a MultiAgent with optional YAML-loaded role configuration.
// If teamFile is provided, roles are loaded from that path; otherwise teams/default.yaml is tried.
func NewMultiAgent(parent *Runner, teamFile string) *MultiAgent {
	var cfg *team.Config
	path := teamFile
	if path == "" {
		path = TeamConfigPath
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if b, err := os.ReadFile(path); err == nil {
		var c team.Config
		if uerr := yaml.Unmarshal(b, &c); uerr == nil && len(c.Roles) > 0 {
			cfg = &c
			log.Printf("[multiagent] loaded team config: %s roles=%d", path, len(c.Roles))
		}
	}
	return &MultiAgent{
		parent:     parent,
		teamCfg:    cfg,
		budgetMgr:  NewBudgetManager(DefaultMultiAgentTokenBudget, DefaultMultiAgentAgentBudget),
		router:     orchestration.NewRouter(),
		blackboard: orchestration.NewBlackboard(),
		journals:   map[string]*orchestration.Journal{},
		journalCfg: orchestration.DefaultJournalStorageConfig(),
	}
}

// SetJournalConfig configures the journal persistence backend.
// Call this before RunDeep/RunParallel to use a non-default storage backend.
func (m *MultiAgent) SetJournalConfig(cfg orchestration.JournalStorageConfig) {
	m.journalCfg = cfg
}

// Blackboard returns the shared inter-agent communication board (P2-1).
func (m *MultiAgent) Blackboard() *orchestration.Blackboard { return m.blackboard }

// Router returns the topology selection router (P2-2).
func (m *MultiAgent) Router() *orchestration.Router { return m.router }

// DecideTopology returns the recommended topology for the given user input (P2-2).
func (m *MultiAgent) DecideTopology(input string) orchestration.OrchestratorMode {
	return m.router.DecideAuto(input)
}

type multiResult struct {
	Role      string
	Output    string
	Err       string
	ToolCalls int
	TokenUsed int
}

// RunParallel launches Eino agents concurrently using dynamic team config (or defaults).
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

	roles := m.resolveRoles(goal)

	publish(&engine.Event{
		Type: engine.EventSubAgent, SubType: "start",
		Content: fmt.Sprintf("Eino multi-agent: %d roles", len(roles)), Timestamp: nowMs(),
	})

	if !m.budgetMgr.TryReserveAgents(len(roles)) {
		return nil, fmt.Errorf("agent budget exceeded: %d requested, %d remaining",
			len(roles), m.budgetMgr.RemainingAgents())
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		outs []multiResult
	)
	totalTools, totalTokens := 0, 0
	defer m.budgetMgr.ReleaseAgents(len(roles))
	for _, r := range roles {
		wg.Add(1)
		go func(role, prompt string, allow []string, maxSteps int) {
			defer wg.Done()
			publish(&engine.Event{
				Type: engine.EventSubAgent, SubType: role,
				Content: "start " + role, Timestamp: nowMs(),
			})
			text, tools, tokens, err := m.runOneMax(ctx, session.ID, prompt, allow, opts.AutoApprove, publish, maxSteps)
			mr := multiResult{Role: role, Output: text, ToolCalls: tools, TokenUsed: tokens}
			if err != nil {
				mr.Err = err.Error()
				if mr.Output == "" {
					mr.Output = err.Error()
				}
			}
			mu.Lock()
			outs = append(outs, mr)
			totalTools += tools
			totalTokens += tokens
			mu.Unlock()
			publish(&engine.Event{
				Type: engine.EventSubAgent, SubType: role,
				Content: "done " + role + ": " + truncate(text, SubAgentDoneMaxChars), Timestamp: nowMs(),
			})
		}(r.role, r.prompt, r.tools, r.maxSteps)
	}
	wg.Wait()

	// merge step (serial general agent)
	var mergeIn strings.Builder
	mergeIn.WriteString("Merge the following agent reports into one actionable answer for the user.\nGoal: " + goal + "\n\n")
	for _, o := range outs {
		mergeIn.WriteString("### " + o.Role + "\n" + o.Output + "\n\n")
	}
	final, mergeTools, mergeTokens, err := m.runOne(ctx, session.ID, mergeIn.String(), nil, opts.AutoApprove, publish)
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
	totalTools += mergeTools
	totalTokens += mergeTokens
	if totalTokens <= 0 {
		// heuristic fallback: all role outputs + merge prompt/answer
		for _, o := range outs {
			totalTokens += common.EstimateTokens(o.Output)
		}
		totalTokens += common.EstimateTokens(final) + common.EstimateTokens(goal)
	}

	m.parent.persistAssistant(ctx, session, final)
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino-multi", "agents": len(outs),
		"toolCalls": totalTools, "tokenEst": totalTokens,
	}, Timestamp: nowMs()})

	return &engine.Result{
		SessionID: session.ID, Response: final, Steps: len(outs) + 1,
		ToolCalls: totalTools, TokenUsed: totalTokens,
	}, nil
}

// RunDeep executes sequential Plan → Act → Reflect (DeepAgent), contrasting parallel Teams.
// Progress is journaled (P1-1) so interrupted runs can be resumed from the last phase.
func (m *MultiAgent) RunDeep(
	ctx context.Context,
	session *sessmodel.Session,
	userInput string,
	publish EventSink,
	opts engine.RunOptions,
) (*engine.Result, error) {
	if m == nil || m.parent == nil {
		return nil, fmt.Errorf("deep-agent unavailable")
	}
	goal := deepagent.StripPrefix(userInput)
	if goal == "" {
		goal = userInput
	}

	runID := "deep-" + session.ID + "-" + commonNowString()
	phases := deepagent.Expand(goal)

	// Attempt to resume from existing journaled state (P1-1).
	var completedPhases map[string]string
	var tokensAlready int
	if state := m.loadJournalState(runID); state != nil {
		if orchestration.IsResumable(state.Status) {
			publish(&engine.Event{
				Type: engine.EventSubAgent, SubType: "deep-resume",
				Content:   fmt.Sprintf("Resuming run=%s after phase=%s status=%s", runID, lastPhase(state.PhasesDone), state.Status),
				Timestamp: nowMs(),
			})
			completedPhases = state.Results
			tokensAlready = state.TokensUsed
		}
	}

	publish(&engine.Event{
		Type: engine.EventSubAgent, SubType: "deep-start",
		Content:   fmt.Sprintf("DeepAgent phases=%d goal=%s", len(phases), truncate(goal, DeepGoalMaxChars)),
		Timestamp: nowMs(),
	})

	journal := m.getOrCreateJournal(runID)
	_ = journal.LogStartRun(runID, goal, int(m.budgetMgr.MaxAgents()))

	var chain strings.Builder
	chain.WriteString("Goal: " + goal + "\n")
	type part struct{ ID, Name, Output string }
	var parts []part
	steps := 0
	totalTools, totalTokens := 0, 0

	// Seed chain with previously completed phases (resume support).
	for id, out := range completedPhases {
		parts = append(parts, part{ID: id, Name: id, Output: out})
		chain.WriteString(fmt.Sprintf("\n### %s (prior)\n%s\n", id, truncate(out, DeepPhaseSummaryMaxChars)))
	}

	for _, ph := range phases {
		if err := ctx.Err(); err != nil {
			_ = journal.LogPause(runID, err.Error())
			return &engine.Result{SessionID: session.ID, Response: "cancelled", ErrorClass: "cancel"}, err
		}

		// Skip phase if already completed in a prior run.
		if _, done := completedPhases[ph.ID]; done {
			continue
		}

		if !m.budgetMgr.ConsumeTokens(common.EstimateTokens(ph.Prompt) + 200) {
			_ = journal.LogFail(runID, ph.ID, "token budget exhausted")
			return &engine.Result{SessionID: session.ID, Response: "token budget exceeded"},
				fmt.Errorf("token budget exhausted at phase %s", ph.ID)
		}

		publish(&engine.Event{
			Type: engine.EventPlan, SubType: ph.ID,
			Content: "DeepAgent phase: " + ph.Name, Timestamp: nowMs(),
		})
		prompt := ph.Prompt + "\n\n## Prior phase notes\n" + chain.String()
		max := ph.MaxSteps
		text, tools, tokens, err := m.runOneMax(ctx, session.ID, prompt, ph.Tools, opts.AutoApprove, publish, max)
		if err != nil && text == "" {
			text = "phase error: " + err.Error()
		}
		if !m.budgetMgr.ConsumeTokens(tokens) {
			log.Printf("[multiagent] token budget exceeded at phase %s: %d tokens", ph.ID, tokens)
		}
		_ = journal.LogPhaseCompletion(runID, ph.ID, text)
		_ = journal.LogTokenUse(runID, tokens)
		parts = append(parts, part{ID: ph.ID, Name: ph.Name, Output: text})
		chain.WriteString(fmt.Sprintf("\n### %s\n%s\n", ph.Name, truncate(text, DeepPhaseSummaryMaxChars)))
		steps++
		totalTools += tools
		totalTokens += tokens
		publish(&engine.Event{
			Type: engine.EventSubAgent, SubType: ph.ID,
			Content: "done " + ph.Name + ": " + truncate(text, DeepPhaseDoneMaxChars), Timestamp: nowMs(),
		})
	}

	// final answer = reflect phase if present, else concat
	final := ""
	if len(parts) > 0 {
		final = strings.TrimSpace(parts[len(parts)-1].Output)
	}
	if final == "" {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString("## " + p.Name + "\n" + p.Output + "\n")
		}
		final = b.String()
	}
	if final == "" {
		final = "DeepAgent finished with empty phases."
	}
	if totalTokens <= 0 {
		totalTokens = common.EstimateTokens(goal) + common.EstimateTokens(final)
		for _, p := range parts {
			totalTokens += common.EstimateTokens(p.Output)
		}
	}
	totalTokens += tokensAlready
	_ = journal.LogComplete(runID, final)

	m.parent.persistAssistant(ctx, session, final)
	publish(&engine.Event{Type: engine.EventAnswer, Content: final, Completed: true, Timestamp: nowMs()})
	publish(&engine.Event{Type: engine.EventDone, Content: final, Completed: true, Data: map[string]any{
		"orchestrator": "eino-deep", "phases": len(parts), "mode": deepagent.ModeDeep,
		"toolCalls": totalTools, "tokenEst": totalTokens,
	}, Timestamp: nowMs()})
	return &engine.Result{
		SessionID: session.ID, Response: final, Steps: steps,
		ToolCalls: totalTools, TokenUsed: totalTokens,
	}, nil
}

// loadJournalState returns the last persisted journal state for a run (or nil).
func (m *MultiAgent) loadJournalState(runID string) *orchestration.JournalState {
	m.journalMu.Lock()
	j, ok := m.journals[runID]
	m.journalMu.Unlock()
	if !ok {
		// Try to create a journal with the configured storage backend for replay.
		j2, err := orchestration.NewJournalWithConfig(m.journalCfg, runID)
		if err != nil {
			return nil
		}
		j = j2
		m.journalMu.Lock()
		m.journals[runID] = j
		m.journalMu.Unlock()
	}
	return j.Replay(runID)
}

// getOrCreateJournal returns (and caches) the journal for a run, using the
// configured storage backend (file/mysql/redis/memory).
func (m *MultiAgent) getOrCreateJournal(runID string) *orchestration.Journal {
	m.journalMu.Lock()
	defer m.journalMu.Unlock()
	if j, ok := m.journals[runID]; ok {
		return j
	}
	j, err := orchestration.NewJournalWithConfig(m.journalCfg, runID)
	if err != nil {
		log.Printf("[multiagent] journal init failed for run=%s: %v; falling back to ephemeral", runID, err)
		j = orchestration.NewEphemeralJournal()
	}
	m.journals[runID] = j
	return j
}

// CloseJournals closes all active journal backends.
func (m *MultiAgent) CloseJournals() {
	m.journalMu.Lock()
	defer m.journalMu.Unlock()
	for id, j := range m.journals {
		if err := j.Close(); err != nil {
			log.Printf("[multiagent] close journal %s: %v", id, err)
		}
	}
	m.journals = map[string]*orchestration.Journal{}
}

// lastPhase returns the last completed phase ID (or empty).
func lastPhase(phases []string) string {
	if len(phases) == 0 {
		return ""
	}
	return phases[len(phases)-1]
}

// commonNowString returns a timestamp-based run-id suffix.
func commonNowString() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// runOne returns (text, toolCalls, tokenUsed, err).
func (m *MultiAgent) runOne(ctx context.Context, sessionID, prompt string, allow []string, auto bool, publish EventSink) (string, int, int, error) {
	return m.runOneMax(ctx, sessionID, prompt, allow, auto, publish, DefaultSubAgentMaxStep)
}

// runOneMax runs a focused ReAct sub-agent and returns measured tool/token stats.
func (m *MultiAgent) runOneMax(ctx context.Context, sessionID, prompt string, allow []string, auto bool, publish EventSink, maxStep int) (string, int, int, error) {
	cm, err := m.parent.newChatModel(ctx)
	if err != nil {
		return "", 0, 0, err
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
	if maxStep <= 0 {
		maxStep = DefaultSubAgentMaxStep
	}
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
		return "", 0, 0, err
	}
	tctx := WithSession(ctx, sessionID, auto)
	stats := &runStats{}
	opt := agentOptions(publish, stats)
	// timeout scales with max steps
	to := SubAgentTimeoutBase + time.Duration(maxStep)*SubAgentTimeoutPerStep
	if to > SubAgentTimeoutMax {
		to = SubAgentTimeoutMax
	}
	cctx, cancel := context.WithTimeout(tctx, to)
	defer cancel()
	out, err := ag.Generate(cctx, []*schema.Message{schema.UserMessage(prompt)}, opt)
	toolN := int(stats.toolCalls.Load())
	tokN := stats.TokenUsed()
	if tokN <= 0 {
		tokN = common.EstimateTokens(prompt)
		if out != nil {
			tokN += common.EstimateTokens(out.Content)
		}
	}
	if err != nil {
		return "", toolN, tokN, err
	}
	if out == nil {
		return "", toolN, tokN, nil
	}
	return strings.TrimSpace(out.Content), toolN, tokN, nil
}

// teamRole is a resolved role for parallel execution.
type teamRole struct {
	role     string
	prompt   string
	tools    []string
	maxSteps int
}

// resolveRoles builds the role list from team config (if loaded) or defaults.
// When teamCfg is available, every role with a description is included, plus a "merge" synthesizer.
func (m *MultiAgent) resolveRoles(goal string) []teamRole {
	if m.teamCfg != nil && len(m.teamCfg.Roles) > 0 {
		var roles []teamRole
		for name, rc := range m.teamCfg.Roles {
			r := teamRole{
				role:     strings.ToLower(name),
				prompt:   rc.Description + "\nGoal:\n" + goal,
				tools:    append([]string{}, rc.Tools...),
				maxSteps: rc.MaxSteps,
			}
			if r.maxSteps <= 0 {
				r.maxSteps = DefaultSubAgentMaxStep
			}
			if len(r.tools) == 0 {
				r.tools = allToolNames()
			}
			roles = append(roles, r)
		}
		// Always append a merge role that only has read + llm tools for synthesis.
		roles = append(roles, teamRole{
			role:   "merge",
			prompt: "Synthesize the following worker outputs into one coherent answer for the user.\nGoal:\n" + goal,
			tools:  []string{"read_file", "grep", "glob"},
		})
		return roles
	}
	// Fallback hardcoded defaults (preserve previous behavior).
	return []teamRole{
		{
			role: "explore", prompt: "Investigate and gather facts (read-only preferred):\n" + goal,
			tools: []string{"read_file", "glob", "grep", "memory_search"}, maxSteps: 8,
		},
		{
			role: "verify", prompt: "Verify findings, list risks and checks:\n" + goal,
			tools: []string{"read_file", "grep", "glob", "bash"}, maxSteps: 6,
		},
	}
}

// allToolNames returns the full tool list (used when team role has empty tools).
func allToolNames() []string {
	return []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep", "memory_search"}
}
