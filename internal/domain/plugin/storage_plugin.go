package plugin

// StoragePlugin is the interface for plugins that provide storage backends.
type StoragePlugin interface {
	ExtendedPlugin

	// Provider returns the storage provider.
	Provider() StorageProvider

	// Backend returns the backend type (e.g., "sqlite", "mysql", "redis").
	Backend() string
}

// StoragePluginBase is the base implementation for storage plugins.
type StoragePluginBase struct {
	id       string
	version  string
	name     string
	ctx      *PluginContext
	provider StorageProvider
	backend  string
}

// NewStoragePluginBase creates a new StoragePluginBase.
func NewStoragePluginBase(id, version, name, backend string) *StoragePluginBase {
	return &StoragePluginBase{
		id:      id,
		version: version,
		name:    name,
		backend: backend,
	}
}

// ID returns the plugin ID.
func (p *StoragePluginBase) ID() string {
	return p.id
}

// Version returns the plugin version.
func (p *StoragePluginBase) Version() string {
	return p.version
}

// Name returns the plugin name (implements Plugin interface).
func (p *StoragePluginBase) Name() string {
	return p.name
}

// Dependencies returns the plugin dependencies (default: none).
func (p *StoragePluginBase) Dependencies() []string {
	return nil
}

// Register registers the plugin (no-op for base implementation).
func (p *StoragePluginBase) Register(reg *Registry) error {
	return nil
}

// Start starts the plugin (default: no-op).
func (p *StoragePluginBase) Start(ctx Context) error {
	return nil
}

// Stop stops the plugin (default: no-op).
func (p *StoragePluginBase) Stop() error {
	return nil
}

// Capabilities returns the plugin capabilities (default: storage).
func (p *StoragePluginBase) Capabilities() []Capability {
	return []Capability{CapabilityStorage}
}

// Init initializes the plugin with the given context.
func (p *StoragePluginBase) Init(ctx *PluginContext) error {
	p.ctx = ctx
	return nil
}

// Provider returns the storage provider.
func (p *StoragePluginBase) Provider() StorageProvider {
	return p.provider
}

// Backend returns the backend type.
func (p *StoragePluginBase) Backend() string {
	return p.backend
}

// SetProvider sets the storage provider.
func (p *StoragePluginBase) SetProvider(provider StorageProvider) {
	p.provider = provider
}
