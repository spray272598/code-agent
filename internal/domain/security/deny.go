package security

import (
	"path/filepath"
	"regexp"
	"strings"
)

type DenyConfig struct {
	ExactPaths   []string `yaml:"exact_paths" json:"exactPaths"`
	GlobPatterns []string `yaml:"glob_patterns" json:"globPatterns"`
}

type DenyEntry struct {
	ExactPaths []string
	GlobMatch  *globMatcher
}

type globMatcher struct {
	patterns []string
}

func (g *globMatcher) Match(path string) bool {
	normalized := filepath.ToSlash(path)
	for _, pattern := range g.patterns {
		if matchGlob(normalized, pattern) {
			return true
		}
	}
	return false
}

func matchGlob(path, pattern string) bool {
	pathParts := strings.Split(path, "/")
	patParts := strings.Split(pattern, "/")
	return matchGlobParts(pathParts, patParts)
}

func matchGlobParts(pathParts, patParts []string) bool {
	for len(patParts) > 0 {
		patPart := patParts[0]
		if patPart == "**" {
			patParts = patParts[1:]
			if len(patParts) == 0 {
				return true
			}
			for i := 0; i <= len(pathParts); i++ {
				if matchGlobParts(pathParts[i:], patParts) {
					return true
				}
			}
			return false
		}
		if len(pathParts) == 0 {
			return false
		}
		if !matchGlobPart(pathParts[0], patPart) {
			return false
		}
		pathParts = pathParts[1:]
		patParts = patParts[1:]
	}
	return len(pathParts) == 0
}

func matchGlobPart(path, pattern string) bool {
	for len(pattern) > 0 {
		if pattern[0] == '*' {
			pattern = pattern[1:]
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if matchGlobPart(path[i:], pattern) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		if pattern[0] == '?' {
			pattern = pattern[1:]
			path = path[1:]
			continue
		}
		if pattern[0] != path[0] {
			return false
		}
		pattern = pattern[1:]
		path = path[1:]
	}
	return len(path) == 0
}

type DenyEngine struct {
	exactMap map[string]bool
	glob     *globMatcher
	patterns []string
}

func NewDenyEngine(config DenyConfig) (*DenyEngine, error) {
	exactMap := make(map[string]bool, len(config.ExactPaths))
	for _, p := range config.ExactPaths {
		normalized := filepath.ToSlash(filepath.Clean(p))
		exactMap[normalized] = true
		exactMap[strings.ToLower(normalized)] = true
	}
	patterns := make([]string, 0, len(config.GlobPatterns))
	for _, p := range config.GlobPatterns {
		if err := validateDenyGlob(p); err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return &DenyEngine{
		exactMap: exactMap,
		glob:     &globMatcher{patterns: patterns},
		patterns: patterns,
	}, nil
}

func (d *DenyEngine) IsDenied(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if d.exactMap[cleaned] || d.exactMap[strings.ToLower(cleaned)] {
		return true
	}
	if d.glob != nil && d.glob.Match(cleaned) {
		return true
	}
	base := filepath.Base(cleaned)
	if d.exactMap[base] || d.exactMap[strings.ToLower(base)] {
		return true
	}
	if d.glob != nil && d.glob.Match(base) {
		return true
	}
	return false
}

func (d *DenyEngine) IsDeniedWithSymlinkAware(path, realPath string) bool {
	if d.IsDenied(path) {
		return true
	}
	if realPath != "" && realPath != path {
		return d.IsDenied(realPath)
	}
	return false
}

func validateDenyGlob(pattern string) error {
	if pattern == "" {
		return nil
	}
	if strings.Contains(pattern, "{") || strings.Contains(pattern, "}") {
		return ErrGlobBraceNotSupported
	}
	if strings.Contains(pattern, "\\") {
		return ErrGlobBackslashNotSupported
	}
	parts := strings.Split(pattern, "/")
	for _, part := range parts {
		if part == "" && pattern != "**" {
			return ErrGlobEmptySegment
		}
		if part == "." || part == ".." {
			return ErrGlobDotDot
		}
	}
	starCount := 0
	for _, p := range strings.Split(pattern, "/") {
		if p == "**" {
			starCount++
		}
	}
	if starCount > 1 {
		for _, p := range strings.Split(pattern, "/") {
			if p == "**" && starCount > 1 {
				return ErrGlobNonStandaloneDoubleStar
			}
		}
	}
	testPath := "/test/file.txt"
	testPattern := "**/*.txt"
	_ = matchGlob(testPath, testPattern)
	return nil
}

var (
	ErrGlobBraceNotSupported       = &GlobValidationError{"brace alternation not supported in deny globs"}
	ErrGlobBackslashNotSupported   = &GlobValidationError{"backslash not supported in deny globs"}
	ErrGlobEmptySegment            = &GlobValidationError{"empty path segment in deny glob"}
	ErrGlobDotDot                  = &GlobValidationError{"dot/dotdot not allowed in deny glob"}
	ErrGlobNonStandaloneDoubleStar = &GlobValidationError{"** must be a complete path segment"}
)

type GlobValidationError struct {
	Msg string
}

func (e *GlobValidationError) Error() string { return "glob validation: " + e.Msg }

func LoadDenyConfig(workspace string) (*DenyConfig, error) {
	config := &DenyConfig{
		ExactPaths: []string{
			".ssh",
			".env",
			"id_rsa",
			"id_dsa",
			"id_ecdsa",
			"id_ed25519",
			"credentials",
			"wallet",
			".aws/credentials",
			".aws/config",
			".kube/config",
			".docker/config.json",
			".npmrc",
			".pypirc",
			".netrc",
			"secrets",
			"secret",
			"keys",
		},
		GlobPatterns: []string{
			"**/*.pem",
			"**/*.key",
			"**/*.p12",
			"**/*.pfx",
			"**/.env*",
			"**/secrets/**",
			"**/credentials.json",
			"**/id_rsa*",
			"**/id_dsa*",
			"**/id_ecdsa*",
			"**/id_ed25519*",
			"**/*.pub",
			"**/wallet/**",
			"**/.ssh/**",
			"**/.aws/**",
			"**/.kube/**",
		},
	}
	return config, nil
}

var (
	denyRegexSsh     = regexp.MustCompile(`(?i)(?:^|[/\\])(\.ssh)(?:$|[/\\])`)
	denyRegexEnv     = regexp.MustCompile(`(?i)(?:^|[/\\])(\.env[^/\\]*)(?:$|[/\\])`)
	denyRegexKey     = regexp.MustCompile(`(?i)(?:^|[/\\])(id_rsa|id_dsa|id_ecdsa|id_ed25519)(?:$|[/\\])`)
	denyRegexCred    = regexp.MustCompile(`(?i)(?:^|[/\\])(credentials)(?:$|[/\\])`)
	denyRegexWallet  = regexp.MustCompile(`(?i)(?:^|[/\\])(wallet)(?:$|[/\\])`)
	denyRegexSecrets = regexp.MustCompile(`(?i)(?:^|[/\\])(secret|secrets)(?:$|[/\\])`)
	denyRegexPem     = regexp.MustCompile(`(?i)\.pem$`)
	denyRegexKeyExt  = regexp.MustCompile(`(?i)\.(?:key|p12|pfx)$`)
	denyRegexPub     = regexp.MustCompile(`(?i)\.pub$`)
)

func (d *DenyEngine) IsDeniedLegacy(path string) bool {
	lp := strings.ToLower(filepath.ToSlash(path))
	if denyRegexSsh.MatchString(lp) || denyRegexEnv.MatchString(lp) ||
		denyRegexKey.MatchString(lp) || denyRegexCred.MatchString(lp) ||
		denyRegexWallet.MatchString(lp) || denyRegexSecrets.MatchString(lp) ||
		denyRegexPem.MatchString(lp) || denyRegexKeyExt.MatchString(lp) ||
		denyRegexPub.MatchString(lp) {
		return true
	}
	return d.IsDenied(path)
}

func DefaultDenyEngine() (*DenyEngine, error) {
	cfg := &DenyConfig{
		ExactPaths: []string{
			".ssh", ".env", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
			"credentials", "wallet", ".aws", ".kube", ".docker",
			"secrets", "secret", "keys",
		},
		GlobPatterns: []string{
			"**/*.pem", "**/*.key", "**/*.p12", "**/*.pfx",
			"**/.env*", "**/secrets/**", "**/credentials.json",
			"**/id_rsa*", "**/id_dsa*", "**/id_ecdsa*", "**/id_ed25519*",
			"**/*.pub", "**/wallet/**", "**/.ssh/**", "**/.aws/**", "**/.kube/**",
		},
	}
	return NewDenyEngine(*cfg)
}
