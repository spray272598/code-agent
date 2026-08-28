// Package pluginruntime provides dynamic plugin loading via Go's plugin system.
// Plugins are compiled as .so shared objects and loaded at runtime.
//
// Plugin .so files must export a symbol:
//
//	var Plugin = &MyPlugin{}  // implements plugin.Plugin or plugin.ExtendedPlugin
//
// Usage:
//
//	loader := pluginruntime.NewSOLoader(pluginCtx)
//	p, err := loader.Load(ctx, "/path/to/plugin.so")
package pluginruntime

import (
	"context"
	"fmt"
	"log/slog"
	"plugin"
	"sync"

	domainplugin "github.com/spray272598/code-agent/internal/domain/plugin"
)

// SOPluginSymbol is the expected export symbol name in .so files.
const SOPluginSymbol = "Plugin"

// SOLoader loads Go plugins from .so shared objects.
// It implements domainplugin.Loader for dynamic plugin loading.
type SOLoader struct {
	mu      sync.RWMutex
	loaded  map[string]*plugin.Plugin
	ctx     *domainplugin.PluginContext
}

// NewSOLoader creates a .so plugin loader.
func NewSOLoader(ctx *domainplugin.PluginContext) *SOLoader {
	return &SOLoader{
		loaded: make(map[string]*plugin.Plugin),
		ctx:    ctx,
	}
}

// Load loads a .so plugin from path and returns the Plugin interface.
func (l *SOLoader) Load(_ context.Context, path string) (domainplugin.Plugin, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if already loaded
	if _, exists := l.loaded[path]; exists {
		return nil, fmt.Errorf("plugin already loaded: %s", path)
	}

	// Open the .so file
	p, err := plugin.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plugin %s: %w", path, err)
	}

	// Look up the Plugin symbol
	sym, err := p.Lookup(SOPluginSymbol)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: symbol %q not found: %w", path, SOPluginSymbol, err)
	}

	// Assert the symbol implements Plugin interface
	dp, ok := sym.(domainplugin.Plugin)
	if !ok {
		return nil, fmt.Errorf("plugin %s: symbol %q does not implement plugin.Plugin", path, SOPluginSymbol)
	}

	l.loaded[path] = p
	slog.Default().Info("plugin loaded",
		"name", dp.Name(),
		"path", path,
		"dependencies", dp.Dependencies(),
	)

	return dp, nil
}

// Unload removes a loaded plugin reference.
func (l *SOLoader) Unload(_ context.Context, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for path, p := range l.loaded {
		// We can't truly unload a Go plugin, but we can remove the reference
		// and let GC reclaim it when no symbols are referenced.
		sym, err := p.Lookup(SOPluginSymbol)
		if err != nil {
			continue
		}
		if dp, ok := sym.(domainplugin.Plugin); ok && dp.Name() == name {
			delete(l.loaded, path)
			slog.Default().Info("plugin unloaded", "name", name, "path", path)
			return nil
		}
	}
	return fmt.Errorf("plugin %q not found in loaded .so plugins", name)
}

// Reload unloads and re-loads a plugin.
func (l *SOLoader) Reload(ctx context.Context, name string) error {
	l.mu.RLock()
	var path string
	for p, pl := range l.loaded {
		sym, err := pl.Lookup(SOPluginSymbol)
		if err != nil {
			continue
		}
		if dp, ok := sym.(domainplugin.Plugin); ok && dp.Name() == name {
			path = p
			break
		}
	}
	l.mu.RUnlock()

	if path == "" {
		return fmt.Errorf("plugin %q not found", name)
	}

	if err := l.Unload(ctx, name); err != nil {
		return err
	}
	_, err := l.Load(ctx, path)
	return err
}

// List returns all loaded plugin names.
func (l *SOLoader) List() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var names []string
	for _, p := range l.loaded {
		sym, err := p.Lookup(SOPluginSymbol)
		if err != nil {
			continue
		}
		if dp, ok := sym.(domainplugin.Plugin); ok {
			names = append(names, dp.Name())
		}
	}
	return names
}
