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
func CommandVariants(raw string) []string {
	if raw == "" {
		return nil
	}
	norm := NormalizeCommand(raw)
	nospace := strings.ReplaceAll(norm, " ", "")
	// also path-normalized slashes
	slash := strings.ReplaceAll(norm, "\\", "/")
	out := []string{raw, strings.ToLower(raw), norm, nospace, slash}
	return uniqueNonEmpty(out)
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
