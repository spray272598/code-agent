package security

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	reMultiSpace = regexp.MustCompile(`\s+`)
	reHexEsc     = regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)
	reOctEsc     = regexp.MustCompile(`\\([0-7]{1,3})`)
	reUnicode    = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
)

// NormalizeCommand reduces common obfuscation used to bypass regex deny lists.
// Variants checked by Guard: raw, normalized, and space-stripped.
func NormalizeCommand(s string) string {
	if s == "" {
		return ""
	}
	out := s
	// URL-decode (possibly twice for double encoding)
	for i := 0; i < 2; i++ {
		if d, err := url.QueryUnescape(out); err == nil && d != out {
			out = d
		} else {
			break
		}
	}
	// Common shell quoting noise
	out = strings.ReplaceAll(out, "`", " ")
	out = strings.ReplaceAll(out, "\"", " ")
	out = strings.ReplaceAll(out, "'", " ")
	// Concatenation tricks: r''m, "r"m → keep letters
	out = stripZeroWidth(out)
	// Escape sequences
	out = reHexEsc.ReplaceAllStringFunc(out, func(m string) string {
		sub := reHexEsc.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		v, err := strconv.ParseUint(sub[1], 16, 8)
		if err != nil {
			return m
		}
		return string(rune(v))
	})
	out = reOctEsc.ReplaceAllStringFunc(out, func(m string) string {
		sub := reOctEsc.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		v, err := strconv.ParseUint(sub[1], 8, 16)
		if err != nil || v > 127 {
			return m
		}
		return string(rune(v))
	})
	out = reUnicode.ReplaceAllStringFunc(out, func(m string) string {
		sub := reUnicode.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		v, err := strconv.ParseUint(sub[1], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(v))
	})
	// ${IFS} / $IFS → space
	out = strings.ReplaceAll(out, "${IFS}", " ")
	out = strings.ReplaceAll(out, "$IFS", " ")
	// Collapse whitespace and lower
	out = reMultiSpace.ReplaceAllString(out, " ")
	out = strings.TrimSpace(strings.ToLower(out))
	return out
}

// CommandVariants returns forms to match against deny/confirm rules.
// Includes whole command plus segments split by shell separators (; | && || \n).
func CommandVariants(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		norm := NormalizeCommand(s)
		// strip separators so "rm;-rf" / "rm| -rf" collapse to "rm-rf"
		nospace := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', ';', '|', '&', '\n', '\r':
				return -1
			default:
				return r
			}
		}, norm)
		// flag-jam: also remove $ and ()
		jam := strings.Map(func(r rune) rune {
			switch r {
			case '$', '(', ')', '{', '}', '[', ']', '`':
				return -1
			default:
				return r
			}
		}, nospace)
		slash := strings.ReplaceAll(norm, "\\", "/")
		out = append(out, s, strings.ToLower(s), norm, nospace, jam, slash)
	}
	add(raw)
	// split compound commands — attacker may hide payload after ;
	for _, seg := range splitShellSegments(raw) {
		add(seg)
	}
	// also normalize then re-split
	for _, seg := range splitShellSegments(NormalizeCommand(raw)) {
		add(seg)
	}
	return uniqueNonEmpty(out)
}

func splitShellSegments(s string) []string {
	// replace common separators with |
	// note: bare "&" only as background if surrounded by spaces to avoid breaking flags
	repl := strings.NewReplacer(
		"&&", "\x00", "||", "\x00", ";", "\x00", "\n", "\x00", "\r", "\x00",
		" | ", "\x00", "|", "\x00",
	)
	s = repl.Replace(s)
	parts := strings.Split(s, "\x00")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripZeroWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff', '\u00a0':
			return -1
		default:
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return ' '
			}
			return r
		}
	}, s)
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
