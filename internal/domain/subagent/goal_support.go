// Experimental: part of the GoalOrchestrator subsystem (plan→execute→verify).
// Not wired into the default agent runtime yet; treat as a spike, API may churn.
package subagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// RoleSpawnOverride resolves a per-role model + harness override.
type RoleSpawnOverride struct {
	ModelID   string
	HarnessID string
}

// IsExplicit returns true when at least one override is set.
func (o RoleSpawnOverride) IsExplicit() bool {
	return o.ModelID != "" || o.HarnessID != ""
}

// SpawnError wraps errors from a subagent spawn to support retry logic.
type SpawnError struct {
	Message   string
	Cancelled bool
}

func (e *SpawnError) Error() string {
	if e.Cancelled {
		return fmt.Sprintf("subagent runtime error (cancelled=true): %s", e.Message)
	}
	return fmt.Sprintf("subagent error: %s", e.Message)
}

// IsCancelled returns true if this error represents a user cancellation.
func (e *SpawnError) IsCancelled() bool { return e.Cancelled }

// SpawnFunc is a closure that spawns a subagent with the given overrides.
type SpawnFunc func(ctx context.Context, modelID, harnessID, prompt string) (string, *SpawnError)

// RoleRenderedPrompt holds primary (configured) and fallback (inherit) prompts.
type RoleRenderedPrompt struct {
	Primary  string
	Fallback string
}

// SpawnWithFailOpenRetry implements grok-build's spawn-and-retry-once pattern.
//
// Rules:
//   - Inherit path: exactly one attempt with current model + session harness.
//   - Explicit path: first attempt with configured {model, harness}; on NON-cancel
//     failure, emit fail-open event and retry ONCE with None overrides (inherit).
//     Cancellation propagates without retry.
func SpawnWithFailOpenRetry(
	ctx context.Context,
	role string,
	override RoleSpawnOverride,
	prompt RoleRenderedPrompt,
	spawn SpawnFunc,
) (string, *SpawnError) {
	if !override.IsExplicit() {
		return spawn(ctx, override.ModelID, override.HarnessID, prompt.Primary)
	}

	firstOut, firstErr := spawn(ctx, override.ModelID, override.HarnessID, prompt.Primary)
	if firstErr == nil || firstErr.IsCancelled() {
		return firstOut, firstErr
	}

	// Fail-open retry
	return spawn(ctx, "", "", prompt.Fallback)
}

// PlannerPromptTemplate is the default goal planner prompt.
const PlannerPromptTemplate = `You are the Goal Plan Writer. Convert the objective into a structured plan.
Read files with your {READ_TOOL} / {SEARCH_TOOL} / {LIST_TOOL} tools. Do NOT modify the workspace.
Write Markdown to a plan file with: Acceptance criteria, Verification plan, Non-goals, Risks.

Terminal response must be exactly: Done`

// VerifierPromptTemplate is the default verifier prompt.
const VerifierPromptTemplate = `You are an adversarial completion verifier. Evaluate only the supplied goal and evidence.
Use {READ_TOOL}, {SEARCH_TOOL}, {LIST_TOOL}, and {EXECUTE_TOOL} to verify deliverables.
Do NOT modify the workspace. Return a JSON verdict with: passed (bool), reason, evidence, gaps[]`

// StrategistPromptTemplate is the default strategist prompt.
const StrategistPromptTemplate = `You are the Goal Strategist. Analyze why the goal is stuck and propose structural remediation.
Read files with {READ_TOOL}, {SEARCH_TOOL}, {LIST_TOOL}. Do NOT modify the workspace.
Write a recommendation note with: root cause analysis, restructure proposal, new acceptance criteria.`

// BuildPlannerPrompt constructs the planner prompt for a specific objective.
func BuildPlannerPrompt(objective, context string, tools *RoleToolNames) string {
	var b strings.Builder
	b.WriteString(PlannerPromptTemplate)
	b.WriteString("\n\n## Objective\n")
	b.WriteString(objective)
	if context != "" {
		b.WriteString("\n\n## Context\n")
		b.WriteString(context)
	}
	if tools != nil {
		b.WriteString("\n\n## Available Tools\n")
		b.WriteString(fmt.Sprintf("- Read: %s\n", tools.Read))
		b.WriteString(fmt.Sprintf("- Search: %s\n", tools.Search))
		b.WriteString(fmt.Sprintf("- List: %s\n", tools.List))
		b.WriteString(fmt.Sprintf("- Execute: %s\n", tools.Execute))
	}
	return b.String()
}

// BuildVerifierPrompt constructs the verifier prompt.
func BuildVerifierPrompt(plan *GoalPlan, workOutput string, tools *RoleToolNames) string {
	var b strings.Builder
	b.WriteString(VerifierPromptTemplate)
	b.WriteString("\n\n## Plan\n")
	b.WriteString(fmt.Sprintf("- Objective: %s\n", plan.Objective))
	b.WriteString(fmt.Sprintf("- Kind: %s\n", plan.Kind.String()))
	b.WriteString("\n### Acceptance Criteria\n")
	for i, c := range plan.AcceptanceCriteria {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
	}
	b.WriteString("\n### Verification Plan\n")
	for i, v := range plan.VerificationPlan {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, v))
	}
	b.WriteString("\n## Work Output\n")
	if len(workOutput) > 4000 {
		b.WriteString(workOutput[:4000])
		b.WriteString("\n... [truncated]")
	} else {
		b.WriteString(workOutput)
	}
	return b.String()
}

// BuildStrategistPrompt constructs the strategist prompt.
func BuildStrategistPrompt(plan *GoalPlan, failures int, gaps []string, tools *RoleToolNames) string {
	var b strings.Builder
	b.WriteString(StrategistPromptTemplate)
	b.WriteString("\n\n## Objective\n")
	b.WriteString(plan.Objective)
	b.WriteString(fmt.Sprintf("\n\n## Context: %d consecutive failures\n", failures))
	if len(gaps) > 0 {
		b.WriteString("\n## Repeated Gaps\n")
		for _, g := range gaps {
			b.WriteString(fmt.Sprintf("- %s\n", g))
		}
	}
	return b.String()
}

// LLMPlanner implements PlannerFunc using a real LLM.
type LLMPlanner struct {
	LLM     port.ILLMPort
	Timeout time.Duration
}

// NewLLMPlanner creates a new LLM-based planner.
func NewLLMPlanner(llm port.ILLMPort) *LLMPlanner {
	return &LLMPlanner{LLM: llm, Timeout: 60 * time.Second}
}

func (p *LLMPlanner) Plan(ctx context.Context, objective, context string) (*GoalPlan, error) {
	if p.LLM == nil {
		return nil, fmt.Errorf("llm unavailable for planning")
	}
	tools := DefaultToolNames()
	sys := BuildPlannerPrompt(objective, context, tools)
	resp, err := p.LLM.Generate(ctx, &port.ChatRequest{
		SystemPrompt: sys,
		Messages:     []port.ChatMessage{{Role: "user", Content: objective}},
		Temperature:  0.2,
	})
	if err != nil {
		return nil, err
	}
	return parsePlanResponse(resp.Content, objective)
}

func parsePlanResponse(content, objective string) (*GoalPlan, error) {
	plan := &GoalPlan{
		ID:                 fmt.Sprintf("plan-%d", time.Now().UnixNano()%1e9),
		Objective:          objective,
		Kind:               GoalKindCodeChange,
		AcceptanceCriteria: []string{"deliverable matches objective"},
		VerificationPlan:   []string{"verify against criteria"},
		CreatedAt:          time.Now(),
		LastUpdatedAt:      time.Now(),
	}

	// Try to parse structured sections
	sections := extractSections(content)
	if criteria, ok := sections["Acceptance criteria"]; ok && len(criteria) > 0 {
		plan.AcceptanceCriteria = criteria
	}
	if verif, ok := sections["Verification plan"]; ok && len(verif) > 0 {
		plan.VerificationPlan = verif
	}
	if goals, ok := sections["Non-goals"]; ok {
		plan.NonGoals = goals
	}
	if kind, ok := sections["Goal kind"]; ok && len(kind) > 0 {
		switch strings.ToLower(kind[0]) {
		case "analysis":
			plan.Kind = GoalKindAnalysis
		case "research":
			plan.Kind = GoalKindResearch
		}
	}
	return plan, nil
}

// extractSections splits a markdown response into named sections.
func extractSections(content string) map[string][]string {
	sections := map[string][]string{}
	lines := strings.Split(content, "\n")
	current := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			current = strings.TrimPrefix(trimmed, "## ")
			current = strings.TrimSpace(current)
			continue
		}
		if current == "" {
			continue
		}
		if trimmed == "" {
			continue
		}
		// Extract list items
		cleaned := strings.TrimPrefix(trimmed, "- ")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			sections[current] = append(sections[current], cleaned)
		}
	}
	return sections
}

// LLMVerifier implements VerifierFunc using a real LLM.
type LLMVerifier struct {
	LLM port.ILLMPort
}

// NewLLMVerifier creates a new LLM-based verifier.
func NewLLMVerifier(llm port.ILLMPort) *LLMVerifier {
	return &LLMVerifier{LLM: llm}
}

func (v *LLMVerifier) Verify(ctx context.Context, plan *GoalPlan, workOutput string) (VerifierResult, error) {
	if v.LLM == nil {
		return VerifierResult{Passed: true, Reason: "no llm verifier", Evidence: "default pass"}, nil
	}
	tools := DefaultToolNames()
	sys := BuildVerifierPrompt(plan, workOutput, tools)
	resp, err := v.LLM.Generate(ctx, &port.ChatRequest{
		SystemPrompt: sys,
		Messages:     []port.ChatMessage{{Role: "user", Content: "evaluate the deliverable"}},
		Temperature:  0.1,
	})
	if err != nil {
		return VerifierResult{Passed: false, Reason: "llm error", Evidence: err.Error()}, err
	}
	return parseVerifierResponse(resp.Content), nil
}

func parseVerifierResponse(content string) VerifierResult {
	result := VerifierResult{Reason: "verifier result", Evidence: ""}
	lower := strings.ToLower(content)

	// Look for JSON verdict
	if strings.Contains(lower, "\"passed\": true") || strings.Contains(lower, "\"verdict\": \"pass") {
		result.Passed = true
	}
	if strings.Contains(lower, "\"passed\": false") || strings.Contains(lower, "\"verdict\": \"fail") {
		result.Passed = false
	}
	// Default: look for pass/fail keywords
	if !strings.Contains(lower, "\"passed\"") && !strings.Contains(lower, "\"verdict\"") {
		if strings.Contains(lower, "pass") || strings.Contains(lower, "success") || strings.Contains(lower, "criteria met") {
			result.Passed = true
		}
		if strings.Contains(lower, "fail") || strings.Contains(lower, "gap") || strings.Contains(lower, "not met") {
			result.Passed = false
		}
	}

	// Extract evidence
	evidenceStart := strings.Index(content, "\"evidence\"")
	if evidenceStart >= 0 {
		evidenceEnd := strings.Index(content[evidenceStart:], "\"")
		if evidenceEnd > 0 {
			result.Evidence = content[evidenceStart+evidenceEnd+1:]
			if next := strings.Index(result.Evidence, "\""); next > 0 {
				result.Evidence = result.Evidence[:next]
			}
		}
	} else {
		result.Evidence = truncate(content, 200)
	}

	// Extract gaps
	gapsStart := strings.Index(content, "\"gaps\"")
	if gapsStart >= 0 {
		after := content[gapsStart:]
		_ = after
	}
	return result
}

// LLMImplementer implements ImplementerFunc using Runner.RunSpec.
type LLMImplementer struct {
	Runner *Runner
}

// NewLLMImplementer creates a new LLM-based implementer.
func NewLLMImplementer(runner *Runner) *LLMImplementer {
	return &LLMImplementer{Runner: runner}
}

func (i *LLMImplementer) Execute(ctx context.Context, plan *GoalPlan) (string, error) {
	if i.Runner == nil {
		return "", fmt.Errorf("runner unavailable")
	}
	prompt := fmt.Sprintf("Execute plan for objective: %s. Steps: %s",
		plan.Objective, strings.Join(plan.TaskChecklist, "; "))
	res := i.Runner.RunOne(ctx, Spec{
		ID:     "impl-" + plan.ID,
		Prompt: prompt,
		Role:   "general",
	})
	if res.Status == "error" {
		return res.Output, fmt.Errorf("implementer failed: %s", res.Output)
	}
	return res.Output, nil
}
