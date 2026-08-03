package security

import (
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// NormalizePathArg hardens path sandbox against common bypasses:
// URL encoding, double encoding, Unicode slash lookalikes, null bytes, trailing dots (Windows).
func NormalizePathArg(p string) string {
	if p == "" {
		return ""
	}
	out := p
	// strip null and control chars early
	out = strings.Map(func(r rune) rune {
		if r == 0 || r == '\u200b' || r == '\ufeff' {
			return -1
		}
		if unicode.IsControl(r) && r != '\t' {
			return -1
		}
		return r
	}, out)

	// URL-decode up to 3 times (double/triple encoding)
	for i := 0; i < 3; i++ {
		if d, err := url.PathUnescape(out); err == nil && d != out {
			out = d
			continue
		}
		if d, err := url.QueryUnescape(out); err == nil && d != out {
			out = d
			continue
		}
		break
	}

	// Unicode slash / path separators → ASCII /
	out = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\',
			'\u2215', // ∕ division slash
			'\u2044', // ⁄ fraction slash
			'\u29F8', // ⧸ big solidus
			'\uFF0F', // ／ fullwidth solidus
			'\uFF3C': // ＼ fullwidth reverse solidus
			return '/'
		case '\u2024': // ․ one dot leader
			return '.'
		case '\uFF0E': // ． fullwidth full stop
			return '.'
		default:
			return r
		}
	}, out)

	// percent-encoded leftovers that failed unescape (malformed)
	out = strings.ReplaceAll(out, "%2e", ".")
	out = strings.ReplaceAll(out, "%2E", ".")
	out = strings.ReplaceAll(out, "%2f", "/")
	out = strings.ReplaceAll(out, "%2F", "/")
	out = strings.ReplaceAll(out, "%5c", "/")
	out = strings.ReplaceAll(out, "%5C", "/")

	// collapse /./ and //
	for strings.Contains(out, "//") {
		out = strings.ReplaceAll(out, "//", "/")
	}
	for strings.Contains(out, "/./") {
		out = strings.ReplaceAll(out, "/./", "/")
	}
	out = strings.TrimSpace(out)

	// Windows trailing dots/spaces in segments are often ignored by FS.
	// Never strip ".." or "." path segments themselves.
	parts := strings.Split(out, "/")
	for i, seg := range parts {
		if seg == ".." || seg == "." || seg == "" {
			continue
		}
		// only trim trailing junk like "file.txt." / "name "
		parts[i] = strings.TrimRight(seg, ". ")
		if parts[i] == "" {
			parts[i] = seg // don't empty a segment that was only dots-with-meaning
		}
	}
	out = strings.Join(parts, "/")

	// invalid UTF-8 → reject by replacing with safe empty marker for later deny
	if !utf8.ValidString(out) {
		return "\x00invalid"
	}
	return out
}

// PathVariants for multi-check against sandbox (raw + normalized + cleaned).
func PathVariants(raw string) []string {
	if raw == "" {
		return nil
	}
	n := NormalizePathArg(raw)
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(n)))
	// also without leading ./
	trim := strings.TrimPrefix(n, "./")
	return uniqueNonEmpty([]string{raw, n, clean, trim, strings.ToLower(n)})
}
