package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// --- Three-Role Tool Architecture ---
//
// Inspired by DeepSeek Harness's capability-seam pattern:
//   - ToolContract: what the model sees (name, description, schema)
//   - ToolProvider: the execution backend
//   - ToolBinding: connects contract to provider with metadata
//
// ITool remains as a convenience interface for backward compatibility.
// New tools can use the three-role API directly.

// ToolContract is the schema/name/description the model sees.
type ToolContract struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	Category    ToolCategory   `json:"category,omitempty"`
}

// ToolProvider is the actual execution backend.
type ToolProvider interface {
	Execute(ctx context.Context, args map[string]any) (Result, error)
}

// ToolBinding connects a contract to a provider with metadata.
type ToolBinding struct {
	Contract ToolContract `json:"contract"`
	Provider ToolProvider `json:"-"`
	Meta     ToolMetadata `json:"meta,omitempty"`
}

// --- Backward Compatibility Adapter ---

// toolAdapter wraps an ITool as a ToolBinding for backward compatibility.
type toolAdapter struct {
	inner ITool
	meta  ToolMetadata
}

func (a *toolAdapter) Execute(ctx context.Context, args map[string]any) (Result, error) {
	return a.inner.Execute(ctx, args)
}

// BindTool wraps an ITool as a ToolBinding.
func BindTool(t ITool, meta ToolMetadata) ToolBinding {
	if meta.Name == "" {
		meta.Name = t.Name()
	}
	return ToolBinding{
		Contract: ToolContract{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
			Category:    meta.Category,
		},
		Provider: &toolAdapter{inner: t, meta: meta},
		Meta:     meta,
	}
}

// --- Directory-Based Discovery ---

// ToolDefinition is the YAML/JSON schema for tool definition files.
type ToolDefinition struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description" yaml:"description"`
	Category    string         `json:"category" yaml:"category"`
	Provider    string         `json:"provider" yaml:"provider"` // "builtin" | "mcp" | "exec"
	Version     string         `json:"version" yaml:"version"`
	Tags        []string       `json:"tags" yaml:"tags"`
	ReadOnly    bool           `json:"readOnly" yaml:"readOnly"`
	Config      map[string]any `json:"config" yaml:"config"`
}

// DiscoverTools scans a directory for tool definition files (*.tool.json, *.tool.yaml).
// Returns parsed definitions. Does not instantiate providers.
func DiscoverTools(dir string) ([]ToolDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan tools dir: %w", err)
	}

	var defs []ToolDefinition
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".json" && ext != ".yaml" && ext != ".yml" {
			continue
		}
		if !strings.HasSuffix(name, ".tool"+ext) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}

		var def ToolDefinition
		switch ext {
		case ".json":
			if err := json.Unmarshal(data, &def); err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
		case ".yaml", ".yml":
			// Use json.Unmarshal as a fallback; yaml would need a dependency
			if err := json.Unmarshal(data, &def); err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
		}

		if def.Name == "" {
			def.Name = strings.TrimSuffix(name, filepath.Ext(name))
			def.Name = strings.TrimSuffix(def.Name, ".tool")
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// --- Agent Definition Discovery ---

// AgentDefinition is the schema for agent definition files (*.agent.json, *.agent.yaml).
type AgentDefinition struct {
	Name         string   `json:"name" yaml:"name"`
	Description  string   `json:"description" yaml:"description"`
	Model        string   `json:"model" yaml:"model"`
	Tools        []string `json:"tools" yaml:"tools"`
	SystemPrompt string   `json:"systemPrompt" yaml:"systemPrompt"`
	Tags         []string `json:"tags" yaml:"tags"`
}

// DiscoverAgents scans a directory for agent definition files.
func DiscoverAgents(dir string) ([]AgentDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan agents dir: %w", err)
	}

	var defs []AgentDefinition
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".json" && ext != ".yaml" && ext != ".yml" {
			continue
		}
		if !strings.HasSuffix(name, ".agent"+ext) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}

		var def AgentDefinition
		if err := json.Unmarshal(data, &def); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}

		if def.Name == "" {
			def.Name = strings.TrimSuffix(name, filepath.Ext(name))
			def.Name = strings.TrimSuffix(def.Name, ".agent")
		}
		defs = append(defs, def)
	}
	return defs, nil
}
