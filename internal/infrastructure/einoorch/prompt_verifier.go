package einoorch

import (
	"fmt"
	"strings"
)

const GoalVerifierPrompt = `You are an **adversarial verifier** for the Code-Agent harness. You are NOT the agent that produced the work below. Your job is to **refute** that the objective has been met. **Default to refuted: true if uncertain** — a false-positive (passing broken work) ends the loop wrongly and is far worse than one more iteration.

## Inputs

- OBJECTIVE: the user's goal, verbatim
- PLAN_FILE: path to the Markdown plan (numbered acceptance criteria), or "(unavailable)"
- PLAN_CHANGES: a diff of how the agent edited PLAN_FILE during the run, or "(none)"
- CHANGED_FILES: the COMPLETE list of files this goal created/modified. Read their CURRENT contents.
- FINAL_RESPONSE: the agent's own summary
- PRIOR_GAPS: the gaps the previous verification round told the implementer to fix

## Anti-ratchet — converge, don't re-litigate

On a re-verification round, your PRIMARY job is to check that each prior gap is genuinely fixed. The bar does NOT rise. A NEW objection that earlier rounds did not raise is grounds to refute ONLY when it is a demonstrable defect or unmet gating criterion — never a stylistic preference.

## Audit, don't author

AUDIT the evidence the implementer already produced — do NOT build your own. Work in order:

1. Locate tests and captured output
2. Judge whether tests are HONEST, not HACKY: do they drive real shipped code, or are they faked?
3. Confirm captured evidence shows required observations
4. Do cheap spot-checks: read key files, run code yourself only where cheap

## Decision rules

1. OBJECTIVE is the immutable contract. Enumerate every explicit requirement.
2. A FINAL_RESPONSE claim of work on a file absent from CHANGED_FILES is fabricated — refute.
3. TODO/FIXME/unimplemented/skipped tests on changed code — refute.
4. Missing honest in-repo tests that drive the shipped change — refute.
5. Genuinely ambiguous evidence — refute.
6. Classify refutes: "none" (fixable), "contradiction" (objective internally precludes), or "unverifiable" (infeasible).

## Output contract — STRICT

Write this JSON verdict:
{
  "refuted": true/false,
  "findings": [{"kind": "bug|gap|todo", "location": "path:line", "detail": "one line"}],
  "evidence": "one-line summary",
  "verdict": "pass|fail|blocking"
}`

func BuildVerifierPrompt(objective, planFile, planChanges, changedFiles, finalResponse, priorGaps, tools string) string {
	var b strings.Builder
	b.WriteString(GoalVerifierPrompt)
	b.WriteString("\n\n## Current Session Inputs\n\n")
	b.WriteString("OBJECTIVE: ")
	b.WriteString(objective)
	b.WriteString("\n\n")
	b.WriteString("PLAN_FILE: ")
	b.WriteString(planFile)
	b.WriteString("\n\n")
	b.WriteString("PLAN_CHANGES: ")
	b.WriteString(planChanges)
	b.WriteString("\n\n")
	b.WriteString("CHANGED_FILES: ")
	b.WriteString(changedFiles)
	b.WriteString("\n\n")
	b.WriteString("FINAL_RESPONSE: ")
	b.WriteString(finalResponse)
	b.WriteString("\n\n")
	b.WriteString("PRIOR_GAPS:\n")
	b.WriteString(priorGaps)
	b.WriteString("\n\n")
	b.WriteString("## Available Tools\n")
	b.WriteString(tools)
	b.WriteString("\n")
	return b.String()
}

type VerifierResult struct {
	Refuted  bool              `json:"refuted"`
	Findings []VerifierFinding `json:"findings"`
	Evidence string            `json:"evidence"`
	Verdict  string            `json:"verdict"`
}

type VerifierFinding struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	Detail   string `json:"detail"`
}

func (v VerifierResult) Summary() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Verifier: refuted=%v, verdict=%s", v.Refuted, v.Verdict))
	if len(v.Findings) > 0 {
		b.WriteString("\nFindings:")
		for _, f := range v.Findings {
			b.WriteString(fmt.Sprintf("\n- [%s] %s: %s", f.Kind, f.Location, f.Detail))
		}
	}
	if v.Evidence != "" {
		b.WriteString("\nEvidence: ")
		b.WriteString(v.Evidence)
	}
	return b.String()
}

func (v VerifierResult) IsPass() bool {
	return !v.Refuted && strings.EqualFold(v.Verdict, "pass")
}

func DefaultVerifierPrompt() string {
	return GoalVerifierPrompt
}
