package plugin

// SecurityPlugin is the interface for plugins that provide security features.
type SecurityPlugin interface {
	ExtendedPlugin

	// Policy returns the security policy.
	Policy() SecurityPolicy

	// Sandbox returns the sandbox interface.
	Sandbox() Sandbox
}

// SecurityPluginBase is the base implementation for security plugins.
type SecurityPluginBase struct {
	id       string
	version  string
	name     string
	ctx      *PluginContext
	policy   SecurityPolicy
	sandbox  Sandbox
}

// NewSecurityPluginBase creates a new SecurityPluginBase.
func NewSecurityPluginBase(id, version, name string) *SecurityPluginBase {
	return &SecurityPluginBase{
		id:      id,
		version: version,
		name:    name,
	}
}

// ID returns the plugin ID.
func (p *SecurityPluginBase) ID() string {
	return p.id
}

// Version returns the plugin version.
func (p *SecurityPluginBase) Version() string {
	return p.version
}

// Name returns the plugin name (implements Plugin interface).
func (p *SecurityPluginBase) Name() string {
	return p.name
}

// Dependencies returns the plugin dependencies (default: none).
func (p *SecurityPluginBase) Dependencies() []string {
	return nil
}

// Register registers the plugin (no-op for base implementation).
func (p *SecurityPluginBase) Register(reg *Registry) error {
	return nil
}

// Start starts the plugin (default: no-op).
func (p *SecurityPluginBase) Start(ctx Context) error {
	return nil
}

// Stop stops the plugin (default: no-op).
func (p *SecurityPluginBase) Stop() error {
	return nil
}

// Capabilities returns the plugin capabilities (default: security).
func (p *SecurityPluginBase) Capabilities() []Capability {
	return []Capability{CapabilitySecurity}
}

// Init initializes the plugin with the given context.
func (p *SecurityPluginBase) Init(ctx *PluginContext) error {
	p.ctx = ctx
	return nil
}

// Policy returns the security policy.
func (p *SecurityPluginBase) Policy() SecurityPolicy {
	return p.policy
}

// Sandbox returns the sandbox interface.
func (p *SecurityPluginBase) Sandbox() Sandbox {
	return p.sandbox
}

// SetPolicy sets the security policy.
func (p *SecurityPluginBase) SetPolicy(policy SecurityPolicy) {
	p.policy = policy
}

// SetSandbox sets the sandbox interface.
func (p *SecurityPluginBase) SetSandbox(sandbox Sandbox) {
	p.sandbox = sandbox
}
