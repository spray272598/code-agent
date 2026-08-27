package plugin

import (
	"context"
)

// Loader is the interface for plugin loaders.
type Loader interface {
	// Load loads a plugin from the given path.
	Load(ctx context.Context, path string) (Plugin, error)

	// LoadFromDir loads all plugins from the given directory.
	LoadFromDir(ctx context.Context, dir string) ([]Plugin, error)

	// Unload unloads a plugin by name.
	Unload(ctx context.Context, name string) error

	// Reload reloads a plugin by name.
	Reload(ctx context.Context, name string) error

	// List lists all loaded plugins.
	List() []Plugin

	// Get returns a plugin by name.
	Get(name string) (Plugin, bool)
}

// PluginManifest represents the manifest file for a plugin.
type PluginManifest struct {
	ID           string            `json:"id" yaml:"id"`
	Version      string            `json:"version" yaml:"version"`
	Name         string            `json:"name" yaml:"name"`
	Description  string            `json:"description" yaml:"description"`
	Author       string            `json:"author" yaml:"author"`
	Entry        string            `json:"entry" yaml:"entry"`
	Type         string            `json:"type" yaml:"type"`
	Dependencies []string          `json:"dependencies" yaml:"dependencies"`
	Capabilities []string          `json:"capabilities" yaml:"capabilities"`
	Config       map[string]any    `json:"config" yaml:"config"`
	Permissions  []string          `json:"permissions" yaml:"permissions"`
}
