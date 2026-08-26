package security

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type SandboxEnforcer interface {
	ApplyProfile(profile ProfileConfig, workspace string) error
	IsActive() bool
	LogViolation(target, operation string)
	Execute(cmd *exec.Cmd) error
}

type platformSandbox interface {
	apply(profile ProfileConfig, workspace string) error
	execute(cmd *exec.Cmd) error
}

type OSLevelSandbox struct {
	mu         sync.Mutex
	active     bool
	profile    *ProfileConfig
	workspace  string
	denyEngine *DenyEngine
	audit      *AuditLogger
	platform   string
	applied    bool
	impl       platformSandbox
}

func NewOSLevelSandbox(audit *AuditLogger) *OSLevelSandbox {
	platform := runtime.GOOS
	s := &OSLevelSandbox{
		platform: platform,
		audit:    audit,
	}
	s.impl = newPlatformSandbox(platform, s)
	return s
}

func (s *OSLevelSandbox) ApplyProfile(profile ProfileConfig, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.profile = &profile
	s.workspace = workspace

	if s.impl != nil {
		if err := s.impl.apply(profile, workspace); err != nil {
			return fmt.Errorf("sandbox apply: %w", err)
		}
	}

	cfg := DenyConfig{
		GlobPatterns: profile.Deny,
	}
	engine, err := NewDenyEngine(cfg)
	if err != nil {
		return fmt.Errorf("deny engine: %w", err)
	}
	s.denyEngine = engine
	s.active = true
	s.applied = true
	return nil
}

func (s *OSLevelSandbox) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *OSLevelSandbox) LogViolation(target, operation string) {
	if s.audit != nil {
		s.audit.Violation("sandbox", "access_denied", target, operation)
	}
}

func (s *OSLevelSandbox) Execute(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	rel, err := filepath.Rel(absW, absP)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func looksNetworkCmd(path, arg string) bool {
	networkCmds := []string{"curl", "wget", "ssh", "scp", "nc", "ncat", "telnet", "netcat"}
	lp := strings.ToLower(filepath.Base(path))
	for _, c := range networkCmds {
		if lp == c {
			return true
		}
	}
	low := strings.ToLower(arg)
	for _, c := range networkCmds {
		if strings.Contains(low, c) {
			return true
		}
	}
	return false
}

type SandboxProfileTier int

const (
	TierWorkspace SandboxProfileTier = iota
	TierReadonly
	TierStrict
	TierDevbox
	TierSandboxed
)

func (t SandboxProfileTier) String() string {
	switch t {
	case TierReadonly:
		return "readonly"
	case TierStrict:
		return "strict"
	case TierDevbox:
		return "devbox"
	case TierSandboxed:
		return "sandboxed"
	default:
		return "workspace"
	}
}

func ParseTierByName(name string) (SandboxProfileTier, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "workspace":
		return TierWorkspace, true
	case "readonly", "read-only", "ro":
		return TierReadonly, true
	case "strict":
		return TierStrict, true
	case "devbox":
		return TierDevbox, true
	case "sandboxed", "sandbox":
		return TierSandboxed, true
	default:
		return TierWorkspace, false
	}
}

func (t SandboxProfileTier) ToSandboxMode() SandboxMode {
	switch t {
	case TierReadonly:
		return ModeReadonly
	case TierStrict:
		return ModeStrict
	default:
		return ModeWorkspace
	}
}

var (
	ErrSandboxUnsupported    = &SandboxError{"sandbox not supported on this platform"}
	ErrSandboxDenied         = &SandboxError{"operation denied by sandbox policy"}
	ErrSandboxNetworkBlocked = &SandboxError{"network access blocked by sandbox policy"}
)

type SandboxError struct {
	Msg string
}

func (e *SandboxError) Error() string { return "sandbox: " + e.Msg }

type SandboxManager struct {
	mu           sync.RWMutex
	enforcers    map[string]*OSLevelSandbox
	configLoader *ConfigLoader
	workspace    string
	audit        *AuditLogger
}

func NewSandboxManager(workspace string, configLoader *ConfigLoader, audit *AuditLogger) *SandboxManager {
	return &SandboxManager{
		enforcers:    make(map[string]*OSLevelSandbox),
		configLoader: configLoader,
		workspace:    workspace,
		audit:        audit,
	}
}

func (m *SandboxManager) Activate(sessionID string, tier SandboxProfileTier) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, ok := m.configLoader.GetProfile(tier.String())
	if !ok {
		profile = defaultProfiles()[tier.String()]
	}

	enforcer := NewOSLevelSandbox(m.audit)
	if err := enforcer.ApplyProfile(profile, m.workspace); err != nil {
		return err
	}
	m.enforcers[sessionID] = enforcer
	return nil
}

func (m *SandboxManager) Deactivate(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.enforcers, sessionID)
}

func (m *SandboxManager) GetEnforcer(sessionID string) *OSLevelSandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enforcers[sessionID]
}
