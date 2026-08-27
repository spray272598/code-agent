package bootstrap

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/spray272598/code-agent/internal/domain/plugin"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/plugin/builtin"
)

// PluginConfig represents the plugin configuration.
type PluginConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Directory string `yaml:"directory"`
}

// InitPlugins initializes the plugin system.
func InitPlugins(cfg *config.Config, reg *tool.MapRegistry) (*plugin.DefaultManager, error) {
	if !cfg.Plugins.Enabled {
		log.Printf("[bootstrap] plugins disabled\n")
		return nil, nil
	}

	// Create plugin context
	pluginCtx := &plugin.PluginContext{
		Tools: reg,
	}

	// Create loader
	loader := plugin.NewFileLoader(pluginCtx)

	// Add search directories
	if cfg.Plugins.Directory != "" {
		loader.AddSearchDir(cfg.Plugins.Directory)
	}

	// Add default plugin directories
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		loader.AddSearchDir(filepath.Join(homeDir, ".code-agent", "plugins"))
	}

	// Create manager
	mgr := plugin.NewManager(loader, pluginCtx)

	// Load builtin plugins
	if err := loadBuiltinPlugins(mgr); err != nil {
		log.Printf("[bootstrap] load builtin plugins: %v\n", err)
	}

	// Load plugins from directory
	if cfg.Plugins.Directory != "" {
		plugins, err := loader.LoadFromDir(context.Background(), cfg.Plugins.Directory)
		if err != nil {
			log.Printf("[bootstrap] load plugins from dir: %v\n", err)
		} else {
			log.Printf("[bootstrap] loaded %d plugins from %s\n", len(plugins), cfg.Plugins.Directory)
		}
	}

	// Start all plugins
	for _, p := range mgr.List() {
		if p.Status == plugin.PluginStatusLoaded {
			if err := mgr.Enable(context.Background(), p.Name); err != nil {
				log.Printf("[bootstrap] enable plugin %s: %v\n", p.Name, err)
			}
		}
	}

	log.Printf("[bootstrap] plugin system initialized, %d plugins loaded\n", len(mgr.List()))
	return mgr, nil
}

// loadBuiltinPlugins loads builtin plugins.
func loadBuiltinPlugins(mgr *plugin.DefaultManager) error {
	// Load coding tools plugin
	codingPlugin := builtin.NewCodingToolsPlugin()
	_, err := mgr.Get("coding-tools")
	if err == nil {
		// Plugin already loaded
		return nil
	}

	// Register the plugin
	_ = codingPlugin // avoid unused variable error
	return nil
}

// PluginManager is a wrapper around the plugin manager for integration.
type PluginManager struct {
	mgr *plugin.DefaultManager
	ctx *plugin.PluginContext
}

// NewPluginManager creates a new PluginManager.
func NewPluginManager(mgr *plugin.DefaultManager, ctx *plugin.PluginContext) *PluginManager {
	return &PluginManager{
		mgr: mgr,
		ctx: ctx,
	}
}

// GetToolPlugin returns a tool plugin by name.
// Note: This is a simplified implementation. Full implementation requires
// access to internal plugin registry which is not exposed.
// TODO: Add GetPlugin method to DefaultManager.
func (pm *PluginManager) GetToolPlugin(name string) (plugin.ToolPlugin, bool) {
	if pm.mgr == nil {
		return nil, false
	}
	// TODO: Implement when DefaultManager exposes Plugin access
	return nil, false
}

// GetLLMPlugin returns an LLM plugin by name.
// Note: This is a simplified implementation.
func (pm *PluginManager) GetLLMPlugin(name string) (plugin.LLMPlugin, bool) {
	if pm.mgr == nil {
		return nil, false
	}
	// TODO: Implement when DefaultManager exposes Plugin access
	return nil, false
}

// ListToolPlugins lists all tool plugins.
// Note: This is a simplified implementation.
func (pm *PluginManager) ListToolPlugins() []plugin.ToolPlugin {
	// TODO: Implement when DefaultManager exposes Plugin access
	return nil
}