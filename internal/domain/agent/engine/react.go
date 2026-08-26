package engine

import (
	"regexp"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// ReActStep is one parsed agent turn: Thought → Action(s) → (later Observation).
type ReActStep struct {
	Thought     string
	Actions     []port.ToolCall
	FinalAnswer string // set when the model answers without tools
	Raw         string
}

var (
	reThoughtLine = regexp.MustCompile(`(?im)^\s*(?:thought|思考)\s*[:：]\s*(.+)$`)
	reActionLine  = regexp.MustCompile(`(?im)^\s*(?:action|动作)\s*[:：]\s*(.+)$`)
	reAnswerLine  = regexp.MustCompile(`(?im)^\s*(?:final\s*answer|answer|最终回答|回答)\s*[:：]\s*(.+)$`)
)

// ParseReAct extracts Thought / Action / FinalAnswer from free-form or structured LLM output.
// Supports:
//  1. Pure JSON tool call(s)  → Action only
//  2. Native ToolCalls on response
//  3. Explicit ReAct lines: Thought: ... / Action: {...} / Final Answer: ...
//  4. Mixed prose + JSON tool block
func ParseReAct(content string, native []port.ToolCall) ReActStep {
	step := ReActStep{Raw: content}
	if len(native) > 0 {
		step.Actions = native
		// still harvest Thought from text if present
		step.Thought = extractThought(content)
		if step.Thought == "" {
			step.Thought = "invoke tool(s)"
		}
		return step
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return step
	}

	// Explicit Final Answer line (no tools)
	if m := reAnswerLine.FindStringSubmatch(content); len(m) > 1 {
		// only if no tool JSON
		if calls := parseToolCalls(content); len(calls) == 0 {
			step.Thought = extractThought(content)
			step.FinalAnswer = strings.TrimSpace(m[1])
			// multi-line answer after marker
			if idx := reAnswerLine.FindStringIndex(content); idx != nil {
				rest := strings.TrimSpace(content[idx[1]:])
				if rest != "" && !strings.HasPrefix(rest, "{") {
					// answer may continue on following lines until Action
					var lines []string
					for _, ln := range strings.Split(content[idx[0]:], "\n") {
						if reActionLine.MatchString(ln) || reThoughtLine.MatchString(ln) {
							break
						}
						if mm := reAnswerLine.FindStringSubmatch(ln); len(mm) > 1 {
							lines = append(lines, mm[1])
							continue
						}
						if len(lines) > 0 {
							lines = append(lines, ln)
						}
					}
					if joined := strings.TrimSpace(strings.Join(lines, "\n")); joined != "" {
						step.FinalAnswer = joined
					}
				}
			}
			return step
		}
	}

	step.Thought = extractThought(content)

	// Action: <json> on a line, or trailing JSON
	if m := reActionLine.FindStringSubmatch(content); len(m) > 1 {
		actBody := strings.TrimSpace(m[1])
		// action may span rest of message after first Action line
		if idx := reActionLine.FindStringIndex(content); idx != nil {
			rest := strings.TrimSpace(content[idx[1]:])
			// drop leading colon content already in m[1]; prefer full rest if it has JSON
			if strings.Contains(rest, "{") {
				// strip the first-line fragment already captured if rest starts with it
				actBody = rest
			}
		}
		if calls := parseToolCalls(actBody); len(calls) > 0 {
			step.Actions = calls
			return step
		}
	}

	// Bare JSON tool call(s)
	if calls := parseToolCalls(content); len(calls) > 0 {
		step.Actions = calls
		if step.Thought == "" {
			// prose before JSON as thought
			if i := strings.Index(content, "{"); i > 0 {
				step.Thought = strings.TrimSpace(content[:i])
			}
		}
		return step
	}

	// Natural language final answer
	step.FinalAnswer = content
	return step
}

func extractThought(content string) string {
	if m := reThoughtLine.FindStringSubmatch(content); len(m) > 1 {
		// collect consecutive thought lines
		var parts []string
		for _, ln := range strings.Split(content, "\n") {
			if mm := reThoughtLine.FindStringSubmatch(ln); len(mm) > 1 {
				parts = append(parts, strings.TrimSpace(mm[1]))
				continue
			}
			if len(parts) > 0 {
				// stop at Action/Answer markers
				if reActionLine.MatchString(ln) || reAnswerLine.MatchString(ln) {
					break
				}
				t := strings.TrimSpace(ln)
				if t != "" && !strings.HasPrefix(t, "{") {
					parts = append(parts, t)
				} else {
					break
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	return ""
}

// FormatObservation builds the Observation message fed back into the ReAct loop.
func FormatObservation(toolName, result string) string {
	return "Observation (" + toolName + "):\n" + result
}

// FormatReActContinue is the user-turn nudge after observations.
func FormatReActContinue(step int, planHint string) string {
	msg := "Continue the ReAct loop.\n" +
		"Emit Thought: <reasoning>\n" +
		"Then either Action: {\"name\":\"...\",\"args\":{...}} or Final Answer: <text>."
	if step > 0 {
		msg = "Step " + itoa(step) + " observations above. " + msg
	}
	if planHint != "" {
		msg += "\n" + planHint
	}
	return msg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
