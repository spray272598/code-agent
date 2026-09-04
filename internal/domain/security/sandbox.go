package security

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// Sandbox errors
var (
	ErrSandboxDenied        = errors.New("sandbox: access denied")
	ErrSandboxNetworkBlocked = errors.New("sandbox: network access blocked")
)

// EnforcementLevel describes how strong the sandbox isolation actually is at
// runtime. It exists so the harness can be HONEST about degradation instead of
// reporting "sandbox active" when only in-process heuristics are in effect.
type EnforcementLevel int

const (
	// LevelNone means no sandbox isolation is in effect.
	LevelNone EnforcementLevel = iota
	// LevelHeuristic means only in-process path/network heuristics screen
	// commands; the OS/kernel isolation mechanism is unavailable.
	LevelHeuristic
	// LevelKernel means a real OS/kernel isolation mechanism is active
	// (bwrap, Landlock, macOS seatbelt, or a Windows Job Object).
	LevelKernel
)

// String renders the enforcement level for logs/status.
func (l EnforcementLevel) String() string {
	switch l {
	case LevelNone:
		return "none"
	case LevelHeuristic:
		return "heuristic"
	case LevelKernel:
		return "kernel"
	default:
		return "unknown"
	}
}

type SandboxEnforcer interface {
	ApplyProfile(profile ProfileConfig, workspace string) error
	IsActive() bool
	LogViolation(target, operation string)
	Execute(cmd *exec.Cmd) error
}

type platformSandbox interface {
	apply(profile ProfileConfig, workspace string) (EnforcementLevel, error)
	execute(cmd *exec.Cmd) error
}

// OSLevelSandbox is the facade for the sandbox executor, holding the concrete implementation.
// The concrete implementation is injected via bootstrap (dependency inversion).
type OSLevelSandbox struct {
	mu         sync.Mutex
	active     bool
	profile    *ProfileConfig
	workspace  string
	denyEngine *DenyEngine
	audit      *AuditLogger
	platform   string
	applied    bool
	level      EnforcementLevel
	impl       platformSandbox
	enhanced   SandboxEnforcer // use interface rather than concrete type
	useEnhanced bool
}

// NewOSLevelSandbox creates a sandbox executor.
// enhancedEnforcer is injected by bootstrap (may be nil).
func NewOSLevelSandbox(audit *AuditLogger, enhancedEnforcer SandboxEnforcer) *OSLevelSandbox {
	platform := runtime.GOOS
	s := &OSLevelSandbox{
		platform: platform,
		audit:    audit,
		enhanced: enhancedEnforcer,
	}

	if enhancedEnforcer != nil {
		s.useEnhanced = true
	} else {
		s.impl = newPlatformSandbox(platform, s)
		s.useEnhanced = false
	}

	return s
}

func (s *OSLevelSandbox) ApplyProfile(profile ProfileConfig, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.profile = &profile
	s.workspace = workspace

	// Prefer enhanced sandbox first
	if s.useEnhanced && s.enhanced != nil {
		if err := s.enhanced.ApplyProfile(profile, workspace); err != nil {
			// Enhanced sandbox failed, fall back to legacy sandbox
			s.useEnhanced = false
			s.enhanced = nil
			s.impl = newPlatformSandbox(s.platform, s)
		} else {
			s.active = true
			s.applied = true
			s.level = LevelKernel
			return nil
		}
	}

	// Legacy sandbox logic
	if s.impl != nil {
		lvl, err := s.impl.apply(profile, workspace)
		if err != nil {
			return fmt.Errorf("sandbox apply: %w", err)
		}
		s.level = lvl
	}

	cfg := DenyConfig{
		GlobPatterns: profile.Deny,
	}
	engine, err := NewDenyEngine(cfg)
	if err != nil {
		return fmt.Errorf("deny engine: %w", err)
	}
	s.denyEngine = engine
	s.active = s.level != LevelNone
	s.applied = true
	return nil
}

func (s *OSLevelSandbox) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.useEnhanced && s.enhanced != nil {
		return s.active && s.enhanced.IsActive()
	}
	return s.active
}

// EnforcementLevel returns the actual isolation strength currently in effect:
// LevelKernel when an OS/kernel mechanism is active, LevelHeuristic when only
// in-process path/network heuristics screen commands, and LevelNone when nothing
// is enforced. This is the honest signal the bootstrap uses for degradation.
func (s *OSLevelSandbox) EnforcementLevel() EnforcementLevel {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.useEnhanced && s.enhanced != nil {
		if s.active && s.enhanced.IsActive() {
			return LevelKernel
		}
		return s.level
	}
	return s.level
}

func (s *OSLevelSandbox) LogViolation(target, operation string) {
	if s.audit != nil {
		s.audit.Violation("sandbox", "access_denied", target, operation)
	}
}

func (s *OSLevelSandbox) Execute(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Prefer enhanced sandbox first
	if s.useEnhanced && s.enhanced != nil {
		return s.enhanced.Execute(cmd)
	}

	// Legacy execution
	if s.impl != nil {
		return s.impl.execute(cmd)
	}
	return s.executeInProcess(cmd)
}

func (s *OSLevelSandbox) executeInProcess(cmd *exec.Cmd) error {
	if s.denyEngine != nil && cmd.Dir != "" {
		if s.denyEngine.IsDenied(cmd.Dir) {
			s.LogViolation(cmd.Dir, "execute")
			return ErrSandboxDenied
		}
		if !isUnderWorkspace(cmd.Dir, s.workspace) {
			s.LogViolation(cmd.Dir, "outside_workspace")
			return ErrSandboxDenied
		}
	}
	if s.profile != nil && s.profile.NetworkBlock {
		for _, arg := range cmd.Args {
			if looksNetworkCmd(cmd.Path, arg) {
				s.LogViolation(arg, "network_blocked")
				return ErrSandboxNetworkBlocked
			}
		}
		if looksNetworkCmd(cmd.Path, cmd.Path) {
			s.LogViolation(cmd.Path, "network_blocked")
			return ErrSandboxNetworkBlocked
		}
	}
	return cmd.Run()
}

func isUnderWorkspace(path, workspace string) bool {
	if workspace == "" {
		return true
	}
	absW, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	absP, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(absP, absW)
}

// networkCmds lists binaries whose sole purpose (or a dominant one) is network
// access. Matched against a basename so "/usr/bin/curl" is caught too.
var networkCmds = map[string]bool{
	"curl": true, "wget": true, "ssh": true, "scp": true, "rsync": true,
	"nc": true, "ncat": true, "netcat": true, "telnet": true, "ftp": true, "sftp": true,
	"dig": true, "nslookup": true, "host": true, "whois": true,
	"ping": true, "traceroute": true, "tracepath": true,
	"rclone": true, "socat": true, "nc.openbsd": true, "sclient": true,
}

// cmdWrappers are prefix programs that merely decorate the real command
// ("sudo curl ...", "timeout 30 wget ..."). They are peeled off so the
// underlying binary can be classified.
var cmdWrappers = map[string]bool{
	"sudo": true, "nohup": true, "timeout": true, "env": true, "nice": true,
	"setsid": true, "stdbuf": true, "xargs": true, "command": true, "time": true,
}

// shellInterpreters run an inline script; their payload needs script-level
// inspection instead of simple binary-name matching.
var shellInterpreters = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"pwsh": true, "powershell": true,
}

var pythonInterpreters = map[string]bool{"python": true, "python3": true, "python2": true, "py": true}
var nodeInterpreters = map[string]bool{"node": true, "nodejs": true, "deno": true, "bun": true, "ts-node": true}

// networkScriptPatterns detect network usage inside inline scripts passed to an
// interpreter via -c / -e / -<<heredoc. Matched case-insensitively.
var networkScriptPatterns = []*regexp.Regexp{
	// Python: import socket / urllib / http.client / requests / httpx / aiohttp
	regexp.MustCompile(`(?i)\b(?:import\s+socket|import\s+urllib|import\s+http\.client|import\s+requests|import\s+httpx|import\s+aiohttp|socket\.socket|urllib\.request|http\.client|requests\.(?:get|post|put|head)|httpx\.(?:get|post)|urlopen)\b`),
	// Node: require('http'|'net'|'dns'|'tls'|'http2'|'dgram') / fetch( / axios / http.get
	regexp.MustCompile(`(?i)(?:require\s*\(\s*['"](?:https?|net|dgram|dns|tls|http2)['"]\s*\)|from\s+['"](?:https?|net|dgram|dns|tls|http2)['"]|\bhttps?\.(?:get|request)\s*\(|\bfetch\s*\(|\baxios\b|\bnode-fetch\b)`),
}

// networkSchemePattern matches an explicit network URL in any argument.
var networkSchemePattern = regexp.MustCompile(`(?i)\b(?:https?|ftps?|sftp|ssh|rsync|git)://`)

// looksNetworkCmd reports whether the given command (cmdPath plus its full
// argument string) appears to reach the network. It is a heuristic used by the
// network-block profile: wrappers are stripped, the real binary is matched by
// name, and inline interpreter scripts are scanned for network APIs.
func looksNetworkCmd(cmdPath, arg string) bool {
	lower := strings.ToLower(arg)

	// 1. The binary itself is a known network tool.
	if networkCmds[filepath.Base(strings.ToLower(cmdPath))] {
		return true
	}

	// 2. An explicit network URL anywhere in the argument list.
	if networkSchemePattern.MatchString(arg) {
		return true
	}

	fields := stripCmdWrappers(strings.Fields(arg))
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(strings.ToLower(fields[0]))
	if networkCmds[base] {
		return true
	}

	// 3. Shell one-liners: /dev/tcp, /dev/udp, or an embedded network tool.
	if shellInterpreters[base] {
		if strings.Contains(lower, "/dev/tcp/") || strings.Contains(lower, "/dev/udp/") {
			return true
		}
		for _, f := range fields[1:] {
			if networkCmds[filepath.Base(strings.ToLower(f))] || networkSchemePattern.MatchString(f) {
				return true
			}
		}
		return false
	}

	// 4. Inline scripts handed to an interpreter.
	if pythonInterpreters[base] || nodeInterpreters[base] {
		for _, p := range networkScriptPatterns {
			if p.MatchString(arg) {
				return true
			}
		}
		return false
	}

	// 5. Fall back to the generic URL scan.
	return networkSchemePattern.MatchString(arg)
}

// stripCmdWrappers removes leading wrapper programs (sudo, nohup, timeout, env,
// nice, ...) together with their own options and operands, returning the tokens
// of the real command.
func stripCmdWrappers(fields []string) []string {
	for len(fields) > 0 {
		if !cmdWrappers[filepath.Base(strings.ToLower(fields[0]))] {
			break
		}
		fields = fields[1:]
		// Skip the wrapper's options and operands: `sudo -u root`, `env FOO=bar`,
		// `timeout 30s`, `nice -n 5`.
		for len(fields) > 0 {
			tok := fields[0]
			if strings.HasPrefix(tok, "-") || strings.Contains(tok, "=") || looksLikeDuration(tok) {
				fields = fields[1:]
				continue
			}
			break
		}
	}
	return fields
}

// looksLikeDuration reports whether tok is a bare duration/number operand such
// as the "30" or "30s" in `timeout 30s curl ...`.
func looksLikeDuration(tok string) bool {
	if tok == "" {
		return false
	}
	body := tok
	if n := len(body); n > 0 {
		switch body[n-1] {
		case 's', 'm', 'h', 'd':
			body = body[:n-1]
		}
	}
	if body == "" {
		return false
	}
	dots := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '.' {
			dots++
			if dots > 1 {
				return false
			}
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}