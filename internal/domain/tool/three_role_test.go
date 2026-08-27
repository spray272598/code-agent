package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBindTool(t *testing.T) {
	inner := &mockSnapshotTool{name: "test"}
	binding := BindTool(inner, ToolMetadata{Category: CategoryRead})

	if binding.Contract.Name != "test" {
		t.Errorf("expected name test, got %s", binding.Contract.Name)
	}
	if binding.Contract.Description != "mock tool" {
		t.Errorf("expected description, got %s", binding.Contract.Description)
	}
	if binding.Contract.Category != CategoryRead {
		t.Errorf("expected category read, got %s", binding.Contract.Category)
	}
	if binding.Meta.Category != CategoryRead {
		t.Errorf("expected meta category read, got %s", binding.Meta.Category)
	}

	// Provider should work
	result, err := binding.Provider.Execute(context.Background(), map[string]any{"input": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "echo: hello" {
		t.Errorf("expected echo: hello, got %s", result.Text)
	}
}

func TestDiscoverTools(t *testing.T) {
	dir := t.TempDir()

	// Create tool definition files
	def1 := ToolDefinition{Name: "my_tool", Description: "A test tool", Category: "exec", Provider: "builtin"}
	data1, _ := json.Marshal(def1)
	os.WriteFile(filepath.Join(dir, "my_tool.tool.json"), data1, 0o644)

	def2 := ToolDefinition{Name: "search_tool", Description: "Search", Category: "search"}
	data2, _ := json.Marshal(def2)
	os.WriteFile(filepath.Join(dir, "search.tool.yaml"), data2, 0o644)

	// Non-tool file should be ignored
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0o644)

	defs, err := DiscoverTools(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 defs, got %d", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["my_tool"] || !names["search_tool"] {
		t.Errorf("unexpected tools: %v", names)
	}
}

func TestDiscoverToolsNonexistent(t *testing.T) {
	defs, err := DiscoverTools("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if defs != nil {
		t.Errorf("expected nil, got %d", len(defs))
	}
}

func TestDiscoverToolsAutoName(t *testing.T) {
	dir := t.TempDir()
	def := ToolDefinition{Description: "auto-named"}
	data, _ := json.Marshal(def)
	os.WriteFile(filepath.Join(dir, "auto_name.tool.json"), data, 0o644)

	defs, err := DiscoverTools(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1, got %d", len(defs))
	}
	if defs[0].Name != "auto_name" {
		t.Errorf("expected auto_name, got %s", defs[0].Name)
	}
}

func TestDiscoverAgents(t *testing.T) {
	dir := t.TempDir()
	def := AgentDefinition{Name: "code-reviewer", Description: "Reviews code", Model: "gpt-4", Tools: []string{"read_file", "grep"}}
	data, _ := json.Marshal(def)
	os.WriteFile(filepath.Join(dir, "code-reviewer.agent.json"), data, 0o644)

	defs, err := DiscoverAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1, got %d", len(defs))
	}
	if defs[0].Name != "code-reviewer" {
		t.Errorf("expected code-reviewer, got %s", defs[0].Name)
	}
	if len(defs[0].Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(defs[0].Tools))
	}
}
