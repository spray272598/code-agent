package skill

import (
	"strings"
)

// SubstitutionContext carries runtime values for variable substitution in
// skill bodies. Mirrors the grok-build-main approach.
type SubstitutionContext struct {
	SkillDir   string
	SessionID  string
	PluginRoot string
	PluginData string
	UserID     string
	Workspace  string
}

// Substitute replaces variables in the skill body with concrete values.
//
// Supported variables:
//
//	$ARGUMENTS        – full argument string
//	$ARGUMENTS[N]     – Nth argument (0-indexed)
//	$N                – shorthand for $ARGUMENTS[N]
//	${SKILL_DIR}      – directory containing SKILL.md
//	${SESSION_ID}     – current session ID
//	${USER_ID}        – current user ID
//	${WORKSPACE}      – current workspace path
//	${PLUGIN_ROOT}    – plugin root dir (for plugin-backed skills)
//	${PLUGIN_DATA}    – plugin data dir
//
// When the body contains an argument token ($ARGUMENTS, $ARGUMENTS[N], or
// $N), the arguments are expanded inline and no "**ARGUMENTS:**" suffix is
// appended. When only metadata tokens (${SKILL_DIR} etc.) are present, the
// arguments are appended as a traditional suffix for backward compatibility.
//
// Unknown $-tokens are left unchanged.
func Substitute(body, args string, ctx SubstitutionContext) string {
	if body == "" {
		return body
	}
	out := body

	argv := splitArgs(args)
	argsSubstituted := false

	// $ARGUMENTS[N] (highest priority – must be before $ARGUMENTS to avoid
	// partial match). Iterate high→low so that $12 is tried before $1.
	for i := len(argv) + 20; i >= 0; i-- {
		pattern := "$ARGUMENTS[" + itoa(i) + "]"
		if strings.Contains(out, pattern) {
			replacement := ""
			if i < len(argv) {
				replacement = argv[i]
			}
			out = strings.ReplaceAll(out, pattern, replacement)
			argsSubstituted = true
		}
	}

	// $N shorthand. Iterate high→low. Only mark argsSubstituted=true
	// when a replacement was actually written (not when the pattern was
	// found but skipped because of digit-boundary rules).
	for i := len(argv) + 20; i >= 0; i-- {
		pattern := "$" + itoa(i)
		if !strings.Contains(out, pattern) {
			continue
		}
		replacement := ""
		if i < len(argv) {
			replacement = argv[i]
		}
		newOut, changed := replaceDollarTokenSafe(out, pattern, replacement)
		if changed {
			argsSubstituted = true
		}
		out = newOut
	}

	// $ARGUMENTS (full string)
	if strings.Contains(out, "$ARGUMENTS") {
		out = strings.ReplaceAll(out, "$ARGUMENTS", args)
		argsSubstituted = true
	}

	// Metadata / path tokens (do NOT set argsSubstituted).
	if ctx.SkillDir != "" {
		out = strings.ReplaceAll(out, "${SKILL_DIR}", ctx.SkillDir)
	}
	if ctx.SessionID != "" {
		out = strings.ReplaceAll(out, "${SESSION_ID}", ctx.SessionID)
	}
	if ctx.UserID != "" {
		out = strings.ReplaceAll(out, "${USER_ID}", ctx.UserID)
	}
	if ctx.Workspace != "" {
		out = strings.ReplaceAll(out, "${WORKSPACE}", ctx.Workspace)
	}
	if ctx.PluginRoot != "" {
		out = strings.ReplaceAll(out, "${PLUGIN_ROOT}", ctx.PluginRoot)
	}
	if ctx.PluginData != "" {
		out = strings.ReplaceAll(out, "${PLUGIN_DATA}", ctx.PluginData)
	}

	// Append **ARGUMENTS:** suffix only when no argument token consumed args.
	if !argsSubstituted && args != "" {
		out = out + "\n\n**ARGUMENTS:** " + args
	}
	return out
}

// splitArgs splits an argument string into whitespace-separated tokens.
func splitArgs(args string) []string {
	if args == "" {
		return nil
	}
	return strings.Fields(args)
}

// itoa converts a small non-negative int to its decimal string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// replaceDollarTokenSafe is like replaceDollarToken but also returns a
// "changed" flag so callers can detect whether an actual substitution happened.
func replaceDollarTokenSafe(s, pattern, replacement string) (string, bool) {
	out := strings.Builder{}
	out.Grow(len(s))
	changed := false
	i := 0
	for i < len(s) {
		if i+len(pattern) > len(s) {
			out.WriteString(s[i:])
			break
		}
		if s[i:i+len(pattern)] == pattern {
			next := i + len(pattern)
			before := byte(0)
			if i > 0 {
				before = s[i-1]
			}
			if (next >= len(s) || !isDigit(s[next])) && (before == 0 || !isDigit(before)) {
				out.WriteString(replacement)
				changed = true
				i = next
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String(), changed
}

// replaceDollarToken is kept for backwards compatibility.
func replaceDollarToken(s, pattern, replacement string) string {
	out, _ := replaceDollarTokenSafe(s, pattern, replacement)
	return out
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
