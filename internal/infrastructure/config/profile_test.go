package config

import (
	"testing"
)

func TestProfileStackResolve(t *testing.T) {
	stack := NewProfileStack()

	// Base layer
	stack.Push(&Profile{
		Name: "base",
		Tools: []ToolProfile{
			{Name: "bash", Enabled: true},
			{Name: "read_file", Enabled: true},
		},
		Permissions: PermissionProfile{Mode: "workspace"},
	})

	// Mode layer overrides permissions
	stack.Push(&Profile{
		Name:        "strict",
		Permissions: PermissionProfile{Mode: "strict"},
		Tools: []ToolProfile{
			{Name: "write_file", Enabled: false}, // disable write
		},
		Sandbox: SandboxProfile{Tier: "strict", NetworkBlock: true},
	})

	merged := stack.Resolve()

	if merged.Permissions.Mode != "strict" {
		t.Errorf("expected strict mode, got %s", merged.Permissions.Mode)
	}
	if !merged.Sandbox.NetworkBlock {
		t.Error("expected network block")
	}
	// Should have 3 tools (bash + read_file from base, write_file from strict)
	if len(merged.Tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(merged.Tools))
	}
}

func TestProfileStackEmpty(t *testing.T) {
	stack := NewProfileStack()
	merged := stack.Resolve()
	if merged.Permissions.Mode != "workspace" {
		t.Errorf("expected default workspace mode, got %s", merged.Permissions.Mode)
	}
}

func TestProfileStackOverride(t *testing.T) {
	stack := NewProfileStack()
	stack.Push(&Profile{
		Name: "base",
		Tools: []ToolProfile{
			{Name: "bash", Enabled: true, Config: map[string]any{"timeout": 30}},
		},
	})
	stack.Push(&Profile{
		Name: "override",
		Tools: []ToolProfile{
			{Name: "bash", Enabled: true, Config: map[string]any{"timeout": 60}},
		},
	})
	merged := stack.Resolve()
	if len(merged.Tools) != 1 {
		t.Fatalf("expected 1 tool after override, got %d", len(merged.Tools))
	}
	if merged.Tools[0].Config["timeout"] != 60 {
		t.Errorf("expected timeout=60 after override, got %v", merged.Tools[0].Config["timeout"])
	}
}

func TestDefaultProfile(t *testing.T) {
	p := DefaultProfile()
	if p.Name != "default" {
		t.Errorf("expected default name, got %s", p.Name)
	}
	if len(p.Tools) == 0 {
		t.Error("expected at least one tool")
	}
}
