package builtin

import (
	"github.com/spray272598/code-agent/internal/domain/plugin"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
)

// CodingToolsPlugin is a plugin that provides coding tools.
type CodingToolsPlugin struct {
	plugin.ToolPluginBase
	ws *coding.Workspace
}

// NewCodingToolsPlugin creates a new CodingToolsPlugin.
func NewCodingToolsPlugin() *CodingToolsPlugin {
	return &CodingToolsPlugin{
		ToolPluginBase: *plugin.NewToolPluginBase(
			"coding-tools",
			"1.0.0",
			"Coding Tools",
		),
	}
}

// Init initializes the plugin.
func (p *CodingToolsPlugin) Init(ctx *plugin.PluginContext) error {
	if err := p.ToolPluginBase.Init(ctx); err != nil {
		return err
	}

	// Create workspace
	p.ws = coding.NewWorkspace("./workspace")

	// Register tools
	p.registerTools()
	return nil
}

// registerTools registers all coding tools.
func (p *CodingToolsPlugin) registerTools() {
	tools := []tool.ITool{
		coding.NewReadFile(p.ws),
		coding.NewWriteFile(p.ws),
		coding.NewEditFile(p.ws),
		coding.NewBash(p.ws, 60),
		coding.NewGlob(p.ws),
		coding.NewGrep(p.ws),
	}

	metadata := []tool.ToolMetadata{
		tool.DefaultMeta(tools[0], tool.CategoryRead),
		tool.DefaultMeta(tools[1], tool.CategoryWrite),
		tool.DefaultMeta(tools[2], tool.CategoryWrite),
		tool.DefaultMeta(tools[3], tool.CategoryExec),
		tool.DefaultMeta(tools[4], tool.CategoryGlob),
		tool.DefaultMeta(tools[5], tool.CategorySearch),
	}

	p.RegisterTools(tools, metadata)
}

// Capabilities returns the plugin capabilities.
func (p *CodingToolsPlugin) Capabilities() []plugin.Capability {
	return []plugin.Capability{plugin.CapabilityTool}
}

// Ensure CodingToolsPlugin implements plugin.ToolPlugin
var _ plugin.ToolPlugin = (*CodingToolsPlugin)(nil)
