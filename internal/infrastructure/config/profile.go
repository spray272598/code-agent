package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Profile defines an agent capability combination, inspired by DeepSeek
// Harness's bundle/profile system. Profiles stack: base → mode → user → overlay.
type Profile struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description" json:"description"`
	Tools       []ToolProfile     `yaml:"tools" json:"tools"`
	Permissions PermissionProfile `yaml:"permissions" json:"permissions"`
	Sandbox     SandboxProfile    `yaml:"sandbox" json:"sandbox"`
	Prompt      PromptProfile     `yaml:"prompt" json:"prompt"`
	Extensions  map[string]any    `yaml:"extensions,omitempty" json:"extensions,omitempty"`
}

// ToolProfile defines a tool's configuration in a profile.
type ToolProfile struct {
	Name    string `yaml:"name" json:"name"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	// Config is tool-specific configuration passed to the tool constructor.
	Config map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

// PermissionProfile defines permission settings.
type PermissionProfile struct {
	Mode       string   `yaml:"mode" json:"mode"` // workspace, readonly, strict, devbox
	WriteTools []string `yaml:"writeTools,omitempty" json:"writeTools,omitempty"`
	ConfirmAll bool     `yaml:"confirmAll,omitempty" json:"confirmAll,omitempty"`
}

// SandboxProfile defines sandbox settings.
type SandboxProfile struct {
	Tier         string `yaml:"tier" json:"tier"` // workspace, readonly, strict, devbox, sandboxed
	NetworkBlock bool   `yaml:"networkBlock,omitempty" json:"networkBlock,omitempty"`
}

// PromptProfile defines prompt customizations.
type PromptProfile struct {
	SystemPrompt  string   `yaml:"systemPrompt,omitempty" json:"systemPrompt,omitempty"`
	ExtraSections []string `yaml:"extraSections,omitempty" json:"extraSections,omitempty"`
}

// ProfileStack represents layered profiles: base → mode → user → overlay.
type ProfileStack struct {
	layers []*Profile
}

// NewProfileStack creates an empty profile stack.
func NewProfileStack() *ProfileStack {
	return &ProfileStack{}
}

// Push adds a profile layer on top of the stack.
func (ps *ProfileStack) Push(p *Profile) {
	ps.layers = append(ps.layers, p)
}

// Resolve merges all layers into a single effective profile.
// Later layers override earlier ones for scalar fields; slices are merged.
func (ps *ProfileStack) Resolve() *Profile {
	merged := &Profile{
		Tools:       make([]ToolProfile, 0),
		Extensions:  make(map[string]any),
		Permissions: PermissionProfile{Mode: "workspace"},
		Sandbox:     SandboxProfile{Tier: "workspace"},
	}
	for _, layer := range ps.layers {
		if layer == nil {
			continue
		}
		if layer.Name != "" {
			merged.Name = layer.Name
		}
		if layer.Description != "" {
			merged.Description = layer.Description
		}
		// Merge tools: later layers override same-name tools
		toolMap := make(map[string]int, len(merged.Tools))
		for i, t := range merged.Tools {
			toolMap[t.Name] = i
		}
		for _, t := range layer.Tools {
			if idx, ok := toolMap[t.Name]; ok {
				merged.Tools[idx] = t
			} else {
				toolMap[t.Name] = len(merged.Tools)
				merged.Tools = append(merged.Tools, t)
			}
		}
		// Override permissions
		if layer.Permissions.Mode != "" {
			merged.Permissions.Mode = layer.Permissions.Mode
		}
		if len(layer.Permissions.WriteTools) > 0 {
			merged.Permissions.WriteTools = layer.Permissions.WriteTools
		}
		if layer.Permissions.ConfirmAll {
			merged.Permissions.ConfirmAll = true
		}
		// Override sandbox
		if layer.Sandbox.Tier != "" {
			merged.Sandbox.Tier = layer.Sandbox.Tier
		}
		if layer.Sandbox.NetworkBlock {
			merged.Sandbox.NetworkBlock = true
		}
		// Override prompt
		if layer.Prompt.SystemPrompt != "" {
			merged.Prompt.SystemPrompt = layer.Prompt.SystemPrompt
		}
		merged.Prompt.ExtraSections = append(merged.Prompt.ExtraSections, layer.Prompt.ExtraSections...)
		// Merge extensions
		for k, v := range layer.Extensions {
			merged.Extensions[k] = v
		}
	}
	return merged
}

// LoadProfileFile loads a profile from a YAML or JSON file.
func LoadProfileFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	ext := filepath.Ext(path)
	var p Profile
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse yaml profile: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse json profile: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported profile format: %s", ext)
	}
	return &p, nil
}

// DefaultProfile returns the default workspace profile.
func DefaultProfile() *Profile {
	return &Profile{
		Name:        "default",
		Description: "Default workspace profile",
		Tools: []ToolProfile{
			{Name: "bash", Enabled: true},
			{Name: "read_file", Enabled: true},
			{Name: "write_file", Enabled: true},
			{Name: "edit_file", Enabled: true},
			{Name: "glob", Enabled: true},
			{Name: "grep", Enabled: true},
		},
		Permissions: PermissionProfile{Mode: "workspace"},
		Sandbox:     SandboxProfile{Tier: "workspace"},
	}
}
