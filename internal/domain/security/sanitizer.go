package security

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
)

const (
	redactedSecret     = "[REDACTED_SECRET]"
	redactedURLValue   = "redacted"
	redactedUserSegment = "<user>"
)

type Sanitizer struct {
	apiKeyPrefix   *regexp.Regexp
	awsAccessKey   *regexp.Regexp
	githubToken    *regexp.Regexp
	vendorToken    *regexp.Regexp
	googleAPIKey   *regexp.Regexp
	pemPrivateKey  *regexp.Regexp
	bearerToken    *regexp.Regexp
	jwtToken       *regexp.Regexp
	urlRegex       *regexp.Regexp
	secretAssign   *regexp.Regexp
	sensitiveParams map[string]bool
	homeDir        string
	usernames      []string
	homeUserRegex  *regexp.Regexp
}

var defaultSanitizer *Sanitizer
var onceSanitizer sync.Once

func DefaultSanitizer() *Sanitizer {
	onceSanitizer.Do(func() {
		defaultSanitizer = newSanitizer()
	})
	return defaultSanitizer
}

func newSanitizer() *Sanitizer {
	s := &Sanitizer{
		sensitiveParams: map[string]bool{
			"access_token":     true,
			"api_key":          true,
			"assertion":        true,
			"auth":             true,
			"client_secret":    true,
			"code":             true,
			"code_verifier":    true,
			"id_token":         true,
			"key":              true,
			"password":         true,
			"refresh_token":    true,
			"requested_token":  true,
			"session_id":       true,
			"state":            true,
			"subject_token":    true,
			"token":            true,
		},
	}
	s.compileRegexes()
	s.loadEnv()
	return s
}

func (s *Sanitizer) compileRegexes() {
	s.apiKeyPrefix = regexp.MustCompile(`\b(?:sk[-_]|xai-)[A-Za-z0-9_-]{20,}`)
	s.awsAccessKey = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	s.githubToken = regexp.MustCompile(`\b(?:gh[opusr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)
	s.vendorToken = regexp.MustCompile(`\b(?:glpat-|xox[abp]-|xapp-)[A-Za-z0-9-]{10,}`)
	s.googleAPIKey = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)
	s.pemPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	s.bearerToken = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]{16,}\b`)
	s.jwtToken = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	s.urlRegex = regexp.MustCompile("https?://[^\\s\"'<>(){}\\[\\],;`]+")
	s.secretAssign = regexp.MustCompile(`(?i)\b(api[key_-]+|access[_-]?token|refresh[_-]?token|id[_-]?token|token|secret|client[_-]secret|password)\b(\s*[:=]\s*)(["']?)[^\s"',&]{8,}`)
	s.homeUserRegex = regexp.MustCompile(`([/\\](?:Users|home)[/\\])([^/\\]+)`)
}

func (s *Sanitizer) loadEnv() {
	if home := os.Getenv("HOME"); home != "" {
		s.homeDir = strings.TrimSpace(home)
	} else if up := os.Getenv("USERPROFILE"); up != "" {
		s.homeDir = strings.TrimSpace(up)
	}
	for _, varName := range []string{"USERNAME", "USER"} {
		if name := os.Getenv(varName); name != "" {
			trimmed := strings.TrimSpace(name)
			if len(trimmed) >= 3 {
				found := false
				for _, u := range s.usernames {
					if strings.EqualFold(u, trimmed) {
						found = true
						break
					}
				}
				if !found {
					s.usernames = append(s.usernames, trimmed)
				}
			}
		}
	}
}

func (s *Sanitizer) HasAnyMatch(input string) bool {
	for _, re := range []*regexp.Regexp{
		s.apiKeyPrefix, s.awsAccessKey, s.githubToken, s.vendorToken,
		s.googleAPIKey, s.pemPrivateKey, s.bearerToken, s.jwtToken,
		s.urlRegex, s.secretAssign,
	} {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

func (s *Sanitizer) RedactSecrets(input string) string {
	if !s.HasAnyMatch(input) {
		return input
	}
	out := s.pemPrivateKey.ReplaceAllString(input, redactedSecret)
	out = s.apiKeyPrefix.ReplaceAllString(out, redactedSecret)
	out = s.awsAccessKey.ReplaceAllString(out, redactedSecret)
	out = s.githubToken.ReplaceAllString(out, redactedSecret)
	out = s.vendorToken.ReplaceAllString(out, redactedSecret)
	out = s.googleAPIKey.ReplaceAllString(out, redactedSecret)
	out = s.bearerToken.ReplaceAllString(out, "Bearer "+redactedSecret)
	out = s.jwtToken.ReplaceAllString(out, redactedSecret)
	out = s.redactURLsIn(out)
	out = s.secretAssign.ReplaceAllString(out, "$1$2$3"+redactedSecret)
	return out
}

func (s *Sanitizer) redactURLsIn(text string) string {
	return s.urlRegex.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		s.redactURL(u)
		return u.String()
	})
}

func (s *Sanitizer) redactURL(u *url.URL) {
	u.User = url.User("")
	u.Fragment = ""

	query := u.Query()
	changed := false
	for k := range query {
		if s.sensitiveParams[strings.ToLower(k)] {
			query.Set(k, redactedURLValue)
			changed = true
		}
	}
	if changed {
		u.RawQuery = query.Encode()
	}
}

func (s *Sanitizer) RedactUserPaths(input string) string {
	result := input
	if s.homeDir != "" && strings.Contains(result, s.homeDir) {
		result = s.replaceHomePrefix(result, s.homeDir)
	}
	if len(s.usernames) > 0 {
		result = s.redactUsernameSegments(result)
	}
	if s.homeDir == "" && len(s.usernames) == 0 {
		result = s.homeUserRegex.ReplaceAllString(result, "${1}"+redactedUserSegment)
	}
	return result
}

func (s *Sanitizer) replaceHomePrefix(input, home string) string {
	var b strings.Builder
	rest := input
	for {
		idx := strings.Index(rest, home)
		if idx < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:idx])
		before := rest[:idx]
		after := rest[idx+len(home):]
		prevOK := len(before) == 0 || isSegmentBoundary(rune(before[len(before)-1]))
		nextOK := len(after) == 0 || isSegmentBoundary(rune(after[0]))
		if prevOK && nextOK {
			b.WriteByte('~')
		} else {
			b.WriteString(home)
		}
		rest = after
	}
	return b.String()
}

func (s *Sanitizer) redactUsernameSegments(value string) string {
	var b strings.Builder
	var buf strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if isSegmentBoundary(rune(ch)) {
			s.pushUsernameSegment(&b, buf.String())
			buf.Reset()
			b.WriteByte(ch)
		} else {
			buf.WriteByte(ch)
		}
	}
	s.pushUsernameSegment(&b, buf.String())
	return b.String()
}

func (s *Sanitizer) pushUsernameSegment(out *strings.Builder, segment string) {
	matched := false
	if isWindows() {
		for _, u := range s.usernames {
			if strings.EqualFold(u, segment) {
				matched = true
				break
			}
		}
	} else {
		for _, u := range s.usernames {
			if u == segment {
				matched = true
				break
			}
		}
	}
	if matched {
		out.WriteString(redactedUserSegment)
	} else {
		out.WriteString(segment)
	}
}

func isSegmentBoundary(c rune) bool {
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.')
}

func isWindows() bool {
	return strings.HasPrefix(strings.ToLower(os.Getenv("OS")), "windows")
}

func (s *Sanitizer) RedactAll(input string) string {
	v1 := s.RedactSecrets(input)
	v2 := s.RedactUserPaths(v1)
	return v2
}

func (s *Sanitizer) WalkStringValues(v any, visit func(string) string) any {
	switch val := v.(type) {
	case string:
		return visit(val)
	case map[string]any:
		for k, v2 := range val {
			val[k] = s.WalkStringValues(v2, visit)
		}
		return val
	case []any:
		for i, v2 := range val {
			val[i] = s.WalkStringValues(v2, visit)
		}
		return val
	default:
		return v
	}
}
