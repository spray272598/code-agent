package plugin

// LLMPlugin is the interface for plugins that provide LLM adapters.
type LLMPlugin interface {
	ExtendedPlugin

	// Adapter returns the LLM adapter.
	Adapter() LLMAdapter

	// Models returns the list of models supported by this adapter.
	Models() []ModelInfo
}

// ModelInfo represents information about a model.
type ModelInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	MaxTokens      int    `json:"maxTokens"`
	SupportsTool   bool   `json:"supportsTool"`
	SupportsStream bool   `json:"supportsStream"`
}

// LLMPluginBase is the base implementation for LLM plugins.
type LLMPluginBase struct {
	id       string
	version  string
	name     string
	ctx      *PluginContext
	adapter  LLMAdapter
	models   []ModelInfo
}

// NewLLMPluginBase creates a new LLMPluginBase.
func NewLLMPluginBase(id, version, name string) *LLMPluginBase {
	return &LLMPluginBase{
		id:      id,
		version: version,
		name:    name,
		models:  make([]ModelInfo, 0),
	}
}

// ID returns the plugin ID.
func (p *LLMPluginBase) ID() string {
	return p.id
}

// Version returns the plugin version.
func (p *LLMPluginBase) Version() string {
	return p.version
}

// Name returns the plugin name (implements Plugin interface).
func (p *LLMPluginBase) Name() string {
	return p.name
}

// Dependencies returns the plugin dependencies (default: none).
func (p *LLMPluginBase) Dependencies() []string {
	return nil
}

// Register registers the plugin (no-op for base implementation).
func (p *LLMPluginBase) Register(reg *Registry) error {
	return nil
}

// Start starts the plugin (default: no-op).
func (p *LLMPluginBase) Start(ctx Context) error {
	return nil
}

// Stop stops the plugin (default: no-op).
func (p *LLMPluginBase) Stop() error {
	return nil
}

// Capabilities returns the plugin capabilities (default: llm).
func (p *LLMPluginBase) Capabilities() []Capability {
	return []Capability{CapabilityLLM}
}

// Init initializes the plugin with the given context.
func (p *LLMPluginBase) Init(ctx *PluginContext) error {
	p.ctx = ctx
	return nil
}

// Adapter returns the LLM adapter.
func (p *LLMPluginBase) Adapter() LLMAdapter {
	return p.adapter
}

// Models returns the list of models supported by this adapter.
func (p *LLMPluginBase) Models() []ModelInfo {
	return p.models
}

// SetAdapter sets the LLM adapter.
func (p *LLMPluginBase) SetAdapter(adapter LLMAdapter) {
	p.adapter = adapter
}

// AddModel adds a model to the list.
func (p *LLMPluginBase) AddModel(model ModelInfo) {
	p.models = append(p.models, model)
}
