package plugin

import (
	"fmt"
	"sync"
)

// Status represents the lifecycle state of a plugin.
type Status string

const (
	StatusRegistered Status = "registered"
	StatusStarting   Status = "starting"
	StatusRunning    Status = "running"
	StatusStopping   Status = "stopping"
	StatusStopped    Status = "stopped"
	StatusFailed     Status = "failed"
)

// Plugin is the interface every plugin must implement.
// Register is called once to set up the plugin; Start/Stop manage its lifecycle.
type Plugin interface {
	// Name returns the unique identifier for this plugin.
	Name() string
	// Dependencies lists plugin names this plugin depends on.
	Dependencies() []string
	// Register is called once during setup. Use it to declare capabilities.
	Register(reg *Registry) error
	// Start is called after all plugins are registered.
	Start(ctx Context) error
	// Stop is called during shutdown.
	Stop() error
}

// Context provides a plugin with access to the registry and shared state.
type Context struct {
	Registry *Registry
}

// Registry manages plugin registration, lifecycle, and dependency resolution.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*entry
	order   []string // registration order
}

type entry struct {
	plugin Plugin
	status Status
	err    error
}

// NewRegistry creates a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]*entry),
	}
}

// Register adds a plugin to the registry. Does not start it.
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q already registered", name)
	}
	r.plugins[name] = &entry{plugin: p, status: StatusRegistered}
	r.order = append(r.order, name)
	return nil
}

// Start initializes all registered plugins in dependency order.
// Continues starting other plugins even if one fails.
func (r *Registry) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	started := make(map[string]bool)
	var firstErr error
	for _, name := range r.order {
		if started[name] {
			continue
		}
		if err := r.startPlugin(name, started); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Registry) startPlugin(name string, started map[string]bool) error {
	e, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}
	if started[name] {
		return nil
	}
	if e.status == StatusFailed {
		return fmt.Errorf("plugin %q failed: %v", name, e.err)
	}

	// Start dependencies first
	for _, dep := range e.plugin.Dependencies() {
		if err := r.startPlugin(dep, started); err != nil {
			e.status = StatusFailed
			e.err = fmt.Errorf("dependency %q failed: %w", dep, err)
			return fmt.Errorf("plugin %q dependency %q failed: %w", name, dep, err)
		}
	}

	e.status = StatusStarting
	ctx := Context{Registry: r}
	if err := e.plugin.Start(ctx); err != nil {
		e.status = StatusFailed
		e.err = err
		return fmt.Errorf("plugin %q start failed: %w", name, err)
	}
	e.status = StatusRunning
	started[name] = true
	return nil
}

// Stop stops all plugins in reverse order.
func (r *Registry) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.order) - 1; i >= 0; i-- {
		name := r.order[i]
		e := r.plugins[name]
		if e.status == StatusRunning {
			e.status = StatusStopping
			_ = e.plugin.Stop()
			e.status = StatusStopped
		}
	}
}

// Get returns a registered plugin by name.
func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.plugins[name]
	if !ok {
		return nil, false
	}
	return e.plugin, true
}

// Status returns the status of a plugin.
func (r *Registry) Status(name string) Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.plugins[name]
	if !ok {
		return ""
	}
	return e.status
}

// List returns all registered plugin names and their statuses.
func (r *Registry) List() map[string]Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]Status, len(r.plugins))
	for name, e := range r.plugins {
		result[name] = e.status
	}
	return result
}

// Unregister removes a plugin and stops it if running.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}
	if e.status == StatusRunning {
		e.status = StatusStopping
		_ = e.plugin.Stop()
		e.status = StatusStopped
	}
	delete(r.plugins, name)
	// Remove from order
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}
