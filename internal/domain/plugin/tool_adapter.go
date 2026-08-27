package plugin

import (
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolPluginAdapter wraps existing tools as a ToolPlugin.
type ToolPluginAdapter struct {
	ToolPluginBase
}

// NewToolPluginAdapter creates a new ToolPluginAdapter.
func NewToolPluginAdapter(id, version, name string, tools []tool.ITool, metadata []tool.ToolMetadata) *ToolPluginAdapter {
	adapter := &ToolPluginAdapter{
		ToolPluginBase: *NewToolPluginBase(id, version, name),
	}

	// Register tools
	if tools != nil {
		adapter.RegisterTools(tools, metadata)
	}

	return adapter
}

// Start starts the plugin and registers tools with the registry.
func (a *ToolPluginAdapter) Start(ctx Context) error {
	// Register all tools with the registry
	for i, t := range a.tools {
		var meta tool.ToolMetadata
		if i < len(a.metadata) {
			meta = a.metadata[i]
		} else {
			meta = tool.DefaultMeta(t, tool.CategoryExec)
		}
		a.registry.RegisterWithMeta(t, meta)
	}
	return nil
}

// Stop stops the plugin and unregisters tools.
func (a *ToolPluginAdapter) Stop() error {
	// Unregister all tools
	for _, t := range a.tools {
		a.registry.Unregister(t.Name())
	}
	return nil
}

// WrapITool wraps a single ITool as a ToolPlugin.
func WrapITool(t tool.ITool, category tool.ToolCategory) *ToolPluginAdapter {
	meta := tool.DefaultMeta(t, category)
	return NewToolPluginAdapter(
		t.Name(),
		"1.0.0",
		t.Name(),
		[]tool.ITool{t},
		[]tool.ToolMetadata{meta},
	)
}

// WrapITools wraps multiple ITools as a ToolPlugin.
func WrapITools(tools []tool.ITool, categories []tool.ToolCategory) *ToolPluginAdapter {
	metadata := make([]tool.ToolMetadata, len(tools))
	for i, t := range tools {
		if i < len(categories) {
			metadata[i] = tool.DefaultMeta(t, categories[i])
		} else {
			metadata[i] = tool.DefaultMeta(t, tool.CategoryExec)
		}
	}

	return NewToolPluginAdapter(
		"wrapped-tools",
		"1.0.0",
		"Wrapped Tools",
		tools,
		metadata,
	)
}

// Ensure ToolPluginAdapter implements ExtendedPlugin
var _ ExtendedPlugin = (*ToolPluginAdapter)(nil)

// Ensure ToolPluginAdapter implements ToolPlugin
var _ ToolPlugin = (*ToolPluginAdapter)(nil)
