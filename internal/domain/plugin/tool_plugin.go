package plugin

import (
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolPlugin is the interface for plugins that provide tools.
type ToolPlugin interface {
	ExtendedPlugin

	// Tools returns the tools provided by this plugin.
	Tools() []tool.ITool

	// ToolMetadata returns the metadata for the tools.
	ToolMetadata() []tool.ToolMetadata
}

// ToolPluginBase is the base implementation for tool plugins.
type ToolPluginBase struct {
	id         string
	version    string
	name       string
	ctx        *PluginContext
	tools      []tool.ITool
	metadata   []tool.ToolMetadata
	registry   *tool.MapRegistry
}

// NewToolPluginBase creates a new ToolPluginBase.
func NewToolPluginBase(id, version, name string) *ToolPluginBase {
	return &ToolPluginBase{
		id:       id,
		version:  version,
		name:     name,
		tools:    make([]tool.ITool, 0),
		metadata: make([]tool.ToolMetadata, 0),
	}
}

// ID returns the plugin ID.
func (p *ToolPluginBase) ID() string {
	return p.id
}

// Version returns the plugin version.
func (p *ToolPluginBase) Version() string {
	return p.version
}

// Name returns the plugin name (implements Plugin interface).
func (p *ToolPluginBase) Name() string {
	return p.name
}

// Dependencies returns the plugin dependencies (default: none).
func (p *ToolPluginBase) Dependencies() []string {
	return nil
}

// Register registers the plugin (no-op for base implementation).
func (p *ToolPluginBase) Register(reg *Registry) error {
	return nil
}

// Start starts the plugin (default: no-op).
func (p *ToolPluginBase) Start(ctx Context) error {
	return nil
}

// Stop stops the plugin (default: no-op).
func (p *ToolPluginBase) Stop() error {
	return nil
}

// Capabilities returns the plugin capabilities (default: tool).
func (p *ToolPluginBase) Capabilities() []Capability {
	return []Capability{CapabilityTool}
}

// Init initializes the plugin with the given context.
func (p *ToolPluginBase) Init(ctx *PluginContext) error {
	p.ctx = ctx
	p.registry = ctx.Tools
	return nil
}

// Tools returns the tools provided by this plugin.
func (p *ToolPluginBase) Tools() []tool.ITool {
	return p.tools
}

// ToolMetadata returns the metadata for the tools.
func (p *ToolPluginBase) ToolMetadata() []tool.ToolMetadata {
	return p.metadata
}

// RegisterTool registers a tool with the plugin.
func (p *ToolPluginBase) RegisterTool(t tool.ITool, meta tool.ToolMetadata) {
	p.tools = append(p.tools, t)
	p.metadata = append(p.metadata, meta)

	// If registry is available, register the tool
	if p.registry != nil {
		p.registry.RegisterWithMeta(t, meta)
	}
}

// RegisterTools registers multiple tools with the plugin.
func (p *ToolPluginBase) RegisterTools(tools []tool.ITool, metadata []tool.ToolMetadata) {
	for i, t := range tools {
		var meta tool.ToolMetadata
		if i < len(metadata) {
			meta = metadata[i]
		} else {
			meta = tool.DefaultMeta(t, tool.CategoryExec)
		}
		p.RegisterTool(t, meta)
	}
}

// UnregisterTool unregisters a tool from the plugin.
func (p *ToolPluginBase) UnregisterTool(name string) {
	// Remove from local list
	for i, t := range p.tools {
		if t.Name() == name {
			p.tools = append(p.tools[:i], p.tools[i+1:]...)
			break
		}
	}

	// Remove from metadata
	for i, m := range p.metadata {
		if m.Name == name {
			p.metadata = append(p.metadata[:i], p.metadata[i+1:]...)
			break
		}
	}

	// Unregister from registry
	if p.registry != nil {
		p.registry.Unregister(name)
	}
}
