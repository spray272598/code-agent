package security

// SandboxManager manages sandbox lifecycle and configuration.
// It wraps a SandboxEnforcer and provides additional management features.
type SandboxManager struct {
	workspace       string
	configLoader    *ConfigLoader
	audit           *AuditLogger
	enforcer        SandboxEnforcer
	profile         *ProfileConfig
	applied         bool
}

// NewSandboxManager creates a new SandboxManager with default OS-level sandbox.
func NewSandboxManager(workspace string, configLoader *ConfigLoader, audit *AuditLogger) *SandboxManager {
	return &SandboxManager{
		workspace:    workspace,
		configLoader: configLoader,
		audit:        audit,
	}
}

// NewSandboxManagerWithEnforcer creates a SandboxManager with an external enforcer.
func NewSandboxManagerWithEnforcer(workspace string, configLoader *ConfigLoader, audit *AuditLogger, enforcer SandboxEnforcer) *SandboxManager {
	return &SandboxManager{
		workspace:    workspace,
		configLoader: configLoader,
		audit:        audit,
		enforcer:     enforcer,
	}
}

// ApplyProfile applies a sandbox profile.
func (m *SandboxManager) ApplyProfile(profile ProfileConfig, workspace string) error {
	m.profile = &profile
	m.workspace = workspace

	if m.enforcer != nil {
		if err := m.enforcer.ApplyProfile(profile, workspace); err != nil {
			if m.audit != nil {
				m.audit.Warn(CategorySandbox, "sandbox", "apply profile failed: "+err.Error())
			}
			return err
		}
		m.applied = true
		return nil
	}

	// Fallback: profile applied at OS level via platform sandbox
	// This is handled by the OSLevelSandbox directly
	m.applied = true
	return nil
}

// IsActive returns whether the sandbox is active.
func (m *SandboxManager) IsActive() bool {
	if m.enforcer != nil {
		return m.enforcer.IsActive()
	}
	return m.applied
}

// GetEnforcer returns the underlying sandbox enforcer.
func (m *SandboxManager) GetEnforcer() SandboxEnforcer {
	return m.enforcer
}

// SetEnforcer sets a new sandbox enforcer.
func (m *SandboxManager) SetEnforcer(enforcer SandboxEnforcer) {
	m.enforcer = enforcer
}

// GetProfile returns the current sandbox profile.
func (m *SandboxManager) GetProfile() *ProfileConfig {
	return m.profile
}

// GetWorkspace returns the workspace path.
func (m *SandboxManager) GetWorkspace() string {
	return m.workspace
}