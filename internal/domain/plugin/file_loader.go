package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileLoader is a file-based plugin loader.
type FileLoader struct {
	mu        sync.RWMutex
	plugins   map[string]Plugin
	ctx       *PluginContext
	searchDirs []string
}

// NewFileLoader creates a new FileLoader.
func NewFileLoader(ctx *PluginContext) *FileLoader {
	return &FileLoader{
		plugins: make(map[string]Plugin),
		ctx:     ctx,
	}
}

// AddSearchDir adds a directory to search for plugins.
func (l *FileLoader) AddSearchDir(dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.searchDirs = append(l.searchDirs, dir)
}

// Load loads a plugin from the given path.
func (l *FileLoader) Load(ctx context.Context, path string) (Plugin, error) {
	// 1. Read manifest file
	manifest, err := l.loadManifest(path)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	// 2. Check dependencies
	if err := l.checkDependencies(manifest); err != nil {
		return nil, fmt.Errorf("check dependencies: %w", err)
	}

	// 3. Load plugin
	p, err := l.loadPlugin(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("load plugin: %w", err)
	}

	// 4. Initialize plugin
	if extPlugin, ok := p.(ExtendedPlugin); ok {
		if err := extPlugin.Init(l.ctx); err != nil {
			return nil, fmt.Errorf("init plugin: %w", err)
		}
	}

	// 5. Register plugin
	l.mu.Lock()
	l.plugins[p.Name()] = p
	l.mu.Unlock()

	return p, nil
}

// LoadFromDir loads all plugins from the given directory.
func (l *FileLoader) LoadFromDir(ctx context.Context, dir string) ([]Plugin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir: %w", err)
	}

	var plugins []Plugin
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".json" && ext != ".yaml" && ext != ".yml" {
			continue
		}

		if !isManifestFile(name) {
			continue
		}

		path := filepath.Join(dir, name)
		p, err := l.Load(ctx, path)
		if err != nil {
			fmt.Printf("[plugin] failed to load %s: %v\n", name, err)
			continue
		}
		plugins = append(plugins, p)
	}

	return plugins, nil
}

// Unload unloads a plugin by name.
func (l *FileLoader) Unload(ctx context.Context, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	p, ok := l.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	// Stop plugin if it's an extended plugin
	if extPlugin, ok := p.(ExtendedPlugin); ok {
		// No explicit stop method in ExtendedPlugin, but we can call Stop if available
		_ = extPlugin
	}

	delete(l.plugins, name)
	return nil
}

// Reload reloads a plugin by name.
func (l *FileLoader) Reload(ctx context.Context, name string) error {
	// Unload the plugin
	if err := l.Unload(ctx, name); err != nil {
		return err
	}

	// Find and reload the plugin
	for _, dir := range l.searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			manifestName := entry.Name()
			if !isManifestFile(manifestName) {
				continue
			}

			// Check if this manifest is for our plugin
			path := filepath.Join(dir, manifestName)
			manifest, err := l.loadManifest(path)
			if err != nil {
				continue
			}

			if manifest.Name == name || manifest.ID == name {
				_, err := l.Load(ctx, path)
				return err
			}
		}
	}

	return fmt.Errorf("plugin %q not found in search directories", name)
}

// List lists all loaded plugins.
func (l *FileLoader) List() []Plugin {
	l.mu.RLock()
	defer l.mu.RUnlock()

	plugins := make([]Plugin, 0, len(l.plugins))
	for _, p := range l.plugins {
		plugins = append(plugins, p)
	}
	return plugins
}

// Get returns a plugin by name.
func (l *FileLoader) Get(name string) (Plugin, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	p, ok := l.plugins[name]
	return p, ok
}

// loadManifest loads a manifest file.
func (l *FileLoader) loadManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var manifest PluginManifest
	ext := filepath.Ext(path)
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, err
		}
	case ".yaml", ".yml":
		// For now, use JSON unmarshal as a fallback
		// In production, use a proper YAML parser
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, err
		}
	}

	return &manifest, nil
}

// checkDependencies checks if all dependencies are satisfied.
func (l *FileLoader) checkDependencies(manifest *PluginManifest) error {
	for _, dep := range manifest.Dependencies {
		if _, ok := l.plugins[dep]; !ok {
			return fmt.Errorf("missing dependency: %s", dep)
		}
	}
	return nil
}

// loadPlugin loads a plugin from a manifest.
func (l *FileLoader) loadPlugin(ctx context.Context, manifest *PluginManifest) (Plugin, error) {
	// For now, create a simple plugin based on the manifest
	// In production, this would load a Go plugin (.so/.dylib/.dll)
	return &manifestPlugin{
		manifest: manifest,
		ctx:      l.ctx,
	}, nil
}

// isManifestFile checks if a file is a manifest file.
func isManifestFile(name string) bool {
	return filepath.Ext(name) == ".json" ||
		filepath.Ext(name) == ".yaml" ||
		filepath.Ext(name) == ".yml"
}

// manifestPlugin is a plugin created from a manifest.
type manifestPlugin struct {
	manifest *PluginManifest
	ctx      *PluginContext
}

func (p *manifestPlugin) Name() string {
	if p.manifest.Name != "" {
		return p.manifest.Name
	}
	return p.manifest.ID
}

func (p *manifestPlugin) Dependencies() []string {
	return p.manifest.Dependencies
}

func (p *manifestPlugin) Register(reg *Registry) error {
	return nil
}

func (p *manifestPlugin) Start(ctx Context) error {
	return nil
}

func (p *manifestPlugin) Stop() error {
	return nil
}
