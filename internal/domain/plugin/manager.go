package plugin

import (
	"context"
	"fmt"
	"sync"
)

// Manager is the interface for plugin managers.
type Manager interface {
	// Load loads a plugin from the given path.
	Load(ctx context.Context, path string) error

	// Unload unloads a plugin by name.
	Unload(ctx context.Context, name string) error

	// Reload reloads a plugin by name.
	Reload(ctx context.Context, name string) error

	// Enable enables a plugin.
	Enable(ctx context.Context, name string) error

	// Disable disables a plugin.
	Disable(ctx context.Context, name string) error

	// List lists all plugins.
	List() []PluginInfo

	// Get returns plugin information by name.
	Get(name string) (*PluginInfo, error)

	// GetPluginsByCapability returns plugins with the given capability.
	GetPluginsByCapability(capability Capability) []Plugin

	// OnPluginLoaded registers a callback for when a plugin is loaded.
	OnPluginLoaded(callback func(Plugin))

	// OnPluginUnloaded registers a callback for when a plugin is unloaded.
	OnPluginUnloaded(callback func(string))
}

// PluginInfo represents information about a plugin.
type PluginInfo struct {
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Author       string            `json:"author"`
	Type         string            `json:"type"`
	Status       PluginStatus      `json:"status"`
	Capabilities []Capability      `json:"capabilities"`
	Dependencies []string          `json:"dependencies"`
	Config       map[string]any    `json:"config"`
}

// PluginStatus represents the status of a plugin.
type PluginStatus string

const (
	PluginStatusLoaded    PluginStatus = "loaded"
	PluginStatusStarted   PluginStatus = "started"
	PluginStatusStopped   PluginStatus = "stopped"
	PluginStatusError     PluginStatus = "error"
	PluginStatusDisabled  PluginStatus = "disabled"
)

// DefaultManager is the default plugin manager implementation.
type DefaultManager struct {
	mu          sync.RWMutex
	loader      Loader
	plugins     map[string]Plugin
	info        map[string]*PluginInfo
	ctx         *PluginContext

	// Event callbacks
	onLoaded   []func(Plugin)
	onUnloaded []func(string)
}

// NewManager creates a new plugin manager.
func NewManager(loader Loader, ctx *PluginContext) *DefaultManager {
	return &DefaultManager{
		loader:  loader,
		plugins: make(map[string]Plugin),
		info:    make(map[string]*PluginInfo),
		ctx:     ctx,
	}
}

// Load loads a plugin from the given path.
func (m *DefaultManager) Load(ctx context.Context, path string) error {
	p, err := m.loader.Load(ctx, path)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.plugins[p.Name()] = p
	m.info[p.Name()] = &PluginInfo{
		Name:         p.Name(),
		Dependencies: p.Dependencies(),
		Status:       PluginStatusLoaded,
	}
	m.mu.Unlock()

	// Trigger callbacks
	for _, cb := range m.onLoaded {
		cb(p)
	}

	return nil
}

// Unload unloads a plugin by name.
func (m *DefaultManager) Unload(ctx context.Context, name string) error {
	m.mu.RLock()
	_, ok := m.plugins[name]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	if err := m.loader.Unload(ctx, name); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.plugins, name)
	delete(m.info, name)
	m.mu.Unlock()

	// Trigger callbacks
	for _, cb := range m.onUnloaded {
		cb(name)
	}

	return nil
}

// Reload reloads a plugin by name.
func (m *DefaultManager) Reload(ctx context.Context, name string) error {
	return m.loader.Reload(ctx, name)
}

// Enable enables a plugin.
func (m *DefaultManager) Enable(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, ok := m.info[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	info.Status = PluginStatusLoaded
	return nil
}

// Disable disables a plugin.
func (m *DefaultManager) Disable(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, ok := m.info[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	info.Status = PluginStatusDisabled
	return nil
}

// List lists all plugins.
func (m *DefaultManager) List() []PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]PluginInfo, 0, len(m.info))
	for _, info := range m.info {
		infos = append(infos, *info)
	}
	return infos
}

// Get returns plugin information by name.
func (m *DefaultManager) Get(name string) (*PluginInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, ok := m.info[name]
	if !ok {
		return nil, fmt.Errorf("plugin %q not found", name)
	}

	return info, nil
}

// GetPluginsByCapability returns plugins with the given capability.
func (m *DefaultManager) GetPluginsByCapability(capability Capability) []Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Plugin
	for _, p := range m.plugins {
		if extPlugin, ok := p.(ExtendedPlugin); ok {
			for _, c := range extPlugin.Capabilities() {
				if c == capability {
					result = append(result, p)
					break
				}
			}
		}
	}
	return result
}

// OnPluginLoaded registers a callback for when a plugin is loaded.
func (m *DefaultManager) OnPluginLoaded(callback func(Plugin)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onLoaded = append(m.onLoaded, callback)
}

// OnPluginUnloaded registers a callback for when a plugin is unloaded.
func (m *DefaultManager) OnPluginUnloaded(callback func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUnloaded = append(m.onUnloaded, callback)
}
