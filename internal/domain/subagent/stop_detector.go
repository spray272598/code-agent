package subagent

import (
	"regexp"
	"strings"
)

// PrematureStopDetector identifies patterns where a subagent declares
// premature completion or gives up, even though the goal is not actually
// finished. This mirrors grok-build's goal_stop_detector.rs panel.
//
// Each regex is locked to a source-string constant by a regression test
// so a later refactor cannot silently swap a pattern out. Two patterns are
// expressed in two stages rather than as a single regex:
//
//  1. CHECK_BACK_LATER — a single-regex form would need a negative lookahead
//     (?!your?\b) that Go's RE2 does not support; we implement the same
//     semantics in two stages (broad first-stage regex + your/you post-filter).
//  2. AGENTS_IN_FLIGHT — broadened to cover loop/cron/babysit continuous
//     patterns and waiting-for-agent hand-offs beyond the original N-agent
//     counter.
//
// Regexes are anchored with ^ (and (?m) for multi-line) so the marker must
// start a line; in-prose mentions like "I can't continue without your input"
// mid-sentence are intentionally ignored.
type PrematureStopDetector struct {
	patterns []stopPattern
}

type stopPattern struct {
	label string
	re    *regexp.Regexp
	// twoStage indicates the pattern requires post-filtering (e.g.
	// check_back_later's your/you rejection).
	twoStage bool
}

// Pattern label constants (stable identifiers for telemetry).
const (
	PatternUnableToProceed = "unable_to_proceed"
	PatternGivingUp        = "giving_up"
	PatternStoppingHere    = "stopping_here"
	PatternAgentsInFlight  = "agents_in_flight"
	PatternCheckBackLater  = "check_back_later"
	PatternVerdictLine     = "verdict_line"
	PatternCommitPushPR    = "commit_push_pr"
	PatternReadyForReview  = "ready_for_review"
	PatternPleaseDeflect   = "please_deflection"
	PatternAgentWillReport = "agent_will_report"
	PatternWaitingFor      = "waiting_for"
	PatternLoopActive      = "loop_active"
)

// NewPrematureStopDetector builds the detector with the expanded panel
// matching grok-build's stop_detector.
func NewPrematureStopDetector() *PrematureStopDetector {
	return &PrematureStopDetector{
		patterns: []stopPattern{
			{
				label: PatternUnableToProceed,
				re:    regexp.MustCompile(`(?im)^I (?:can(?:'?t|not)|am unable to) (?:proceed|continue|make (?:any )?progress|complete|fix this)\b`),
			},
			{
				label: PatternGivingUp,
				re:    regexp.MustCompile(`(?im)^(?:Giving up|I(?:'m| am) giving up|The task is not actionable)\b`),
			},
			{
				label: PatternStoppingHere,
				// Trailer widened to include `,`, `;`, ` for ` so naturally
				// occurring sign-offs like "Stopping here for now." still fire.
				re: regexp.MustCompile(`(?im)^(?:Stopping here|I've stopped here|Parked (?:the|this) branch|Paused here)(?:\.|,|;|$| for | —| -| until| pending| since| because)`),
			},
			{
				label: PatternAgentsInFlight,
				// Broadened: original N-agent counter + loop/cron continuous
				// patterns + waiting-for-agent hand-offs.
				re: regexp.MustCompile(`(?im)^(?:(?:\*\*)?[1-9]\d* (?:agent|cron|task|fork|job|worker|PR|check)s? (?:in flight|remaining|active|still (?:running|working)|pending|running|launched)\b|(?:Continuous )?(?:[Ll]oop|[Cc]rons?|[Bb]abysit) (?:active|healthy|continuing|running|will keep|continues)\b|Waiting for (?:the )?(?:agent|cron|task|fork|worker|job|remaining|them)s?\b|Agents? will report back\b|Waiting\.?$)`),
			},
			{
				label:    PatternCheckBackLater,
				twoStage: true,
				// First-stage regex; post-filter in checkBackLaterMatches decides
				// whether the deferral target is the user (you/your) or the system.
				re: regexp.MustCompile(`(?im)^(?:I will|I'll|Will) (?:check back|re-?check|poll|look again|retry|re-?run|try again) (?:in\b|again\b|(?:when|once|after|until)\s+(\S+))`),
			},
			{
				label: PatternVerdictLine,
				re:    regexp.MustCompile(`(?im)^(?:VERDICT|VERDICT RESULT):\s*(?:PASS|FAIL|COMPLETE|INCOMPLETE)`),
			},
			{
				label: PatternCommitPushPR,
				re:    regexp.MustCompile(`(?im)^(?:Pushed (?:to \x60|\x60[0-9a-f]{7,})|Committed as \x60?[0-9a-f]{7,}\b|Commit: \x60?[0-9a-f]{7,}\b|(?:Opened|Created) PR #?\d)`),
			},
			{
				label: PatternReadyForReview,
				re:    regexp.MustCompile(`(?im)^Ready (?:for review|to (?:upload|merge|ship|land))\b`),
			},
			{
				label: PatternPleaseDeflect,
				re:    regexp.MustCompile(`(?im)^Please (?:start|run|provide|grant|export|add|install|configure|give me|paste|point me|set (?:the |up |\x60?[A-Z][A-Z0-9_]+\b))`),
			},
		},
	}
}

// Detect checks the LAST non-empty paragraph for bail patterns. Every line
// in the last paragraph is trimmed and matched against each pattern
// individually (patterns are ^-anchored). If the last paragraph has no
// match, fall back to checking the first paragraph as well, since some
// models emit the bail signal at the very beginning. Returns the label
// of the first matched pattern, or "" if no match.
func (d *PrematureStopDetector) Detect(text string) string {
	normalized := normalizeLineEndings(text)
	paragraphs := splitParagraphs(normalized)
	if len(paragraphs) == 0 {
		return ""
	}

	// Check last paragraph (highest signal).
	if label := d.checkParagraph(paragraphs[len(paragraphs)-1]); label != "" {
		return label
	}

	// Fall back to first paragraph for models that emit bail signal early.
	if len(paragraphs) > 1 {
		return d.checkParagraph(paragraphs[0])
	}
	return ""
}

// checkParagraph matches every line in the paragraph against each pattern.
// Two-stage patterns (check_back_later) require the post-filter to pass.
func (d *PrematureStopDetector) checkParagraph(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, p := range d.patterns {
			if !p.re.MatchString(line) {
				continue
			}
			if p.twoStage {
				if !checkBackLaterMatches(line, p.re) {
					continue
				}
			}
			return p.label
		}
	}
	return ""
}

// checkBackLaterMatches applies the post-filter for the two-stage
// check_back_later pattern. The broad regex captures the trailing token
// after when|once|after|until; the equivalent negative-lookahead means
// "the trailing token does not start a your? word". you/your followed by
// a non-word character is a deferral to the user → should NOT be flagged.
func checkBackLaterMatches(line string, re *regexp.Regexp) bool {
	caps := re.FindStringSubmatch(line)
	if caps == nil {
		return false
	}
	// caps[1] is the captured trailing token; `in`/`again` branches have
	// no capture group — always a bail.
	if len(caps) < 2 || caps[1] == "" {
		return true
	}
	return !isUserPronoun(caps[1])
}

// isUserPronoun returns true if token is `your` or `you` immediately
// followed by a non-word character (or end-of-string). Anything longer
// (`yours`, `your_team`) is not the negative branch.
func isUserPronoun(token string) bool {
	lower := strings.ToLower(token)
	for _, stem := range []string{"your", "you"} {
		if lower == stem {
			return true
		}
		if strings.HasPrefix(lower, stem) && len(lower) > len(stem) {
			rest := lower[len(stem):]
			for _, r := range rest {
				if !isASCIINonAlnum(r) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func isASCIINonAlnum(r rune) bool {
	if r == '_' {
		return false
	}
	if r >= 'a' && r <= 'z' {
		return false
	}
	if r >= 'A' && r <= 'Z' {
		return false
	}
	if r >= '0' && r <= '9' {
		return false
	}
	return true
}

// AllPatterns returns the list of configured pattern labels.
func (d *PrematureStopDetector) AllPatterns() []string {
	out := make([]string, len(d.patterns))
	for i, p := range d.patterns {
		out[i] = p.label
	}
	return out
}

// normalizeLineEndings converts \r\n and bare \r to \n.
func normalizeLineEndings(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// splitParagraphs splits text into paragraphs (double-newline separated).
func splitParagraphs(text string) []string {
	parts := strings.Split(text, "\n\n")
	var out []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
