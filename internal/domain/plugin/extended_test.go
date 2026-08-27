package plugin

import (
	"testing"
)

func TestPluginContext(t *testing.T) {
	ctx := &PluginContext{}
	if ctx == nil {
		t.Error("PluginContext should not be nil")
	}
}

func TestToolPluginBase(t *testing.T) {
	p := NewToolPluginBase("test", "1.0.0", "Test Plugin")
	if p == nil {
		t.Fatal("ToolPluginBase should not be nil")
	}

	if p.ID() != "test" {
		t.Errorf("expected ID 'test', got %s", p.ID())
	}

	if p.Version() != "1.0.0" {
		t.Errorf("expected Version '1.0.0', got %s", p.Version())
	}

	if p.Name() != "Test Plugin" {
		t.Errorf("expected Name 'Test Plugin', got %s", p.Name())
	}

	caps := p.Capabilities()
	if len(caps) != 1 || caps[0] != CapabilityTool {
		t.Errorf("expected capabilities [tool], got %v", caps)
	}
}

func TestLLMPluginBase(t *testing.T) {
	p := NewLLMPluginBase("llm-test", "1.0.0", "Test LLM Plugin")
	if p == nil {
		t.Fatal("LLMPluginBase should not be nil")
	}

	if p.ID() != "llm-test" {
		t.Errorf("expected ID 'llm-test', got %s", p.ID())
	}

	caps := p.Capabilities()
	if len(caps) != 1 || caps[0] != CapabilityLLM {
		t.Errorf("expected capabilities [llm], got %v", caps)
	}

	// Test adding models
	p.AddModel(ModelInfo{
		ID:       "gpt-4",
		Name:     "GPT-4",
		Provider: "openai",
	})

	models := p.Models()
	if len(models) != 1 {
		t.Errorf("expected 1 model, got %d", len(models))
	}
}

func TestStoragePluginBase(t *testing.T) {
	p := NewStoragePluginBase("storage-test", "1.0.0", "Test Storage", "sqlite")
	if p == nil {
		t.Fatal("StoragePluginBase should not be nil")
	}

	if p.Backend() != "sqlite" {
		t.Errorf("expected backend 'sqlite', got %s", p.Backend())
	}

	caps := p.Capabilities()
	if len(caps) != 1 || caps[0] != CapabilityStorage {
		t.Errorf("expected capabilities [storage], got %v", caps)
	}
}

func TestSecurityPluginBase(t *testing.T) {
	p := NewSecurityPluginBase("security-test", "1.0.0", "Test Security")
	if p == nil {
		t.Fatal("SecurityPluginBase should not be nil")
	}

	caps := p.Capabilities()
	if len(caps) != 1 || caps[0] != CapabilitySecurity {
		t.Errorf("expected capabilities [security], got %v", caps)
	}
}

func TestFileLoader(t *testing.T) {
	ctx := &PluginContext{}
	loader := NewFileLoader(ctx)

	// Test adding search directory
	loader.AddSearchDir("test-dir")

	// Test listing plugins
	plugins := loader.List()
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}
}

func TestDefaultManager(t *testing.T) {
	ctx := &PluginContext{}
	loader := NewFileLoader(ctx)
	mgr := NewManager(loader, ctx)

	// Test listing plugins
	plugins := mgr.List()
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}

	// Test getting plugin info
	_, err := mgr.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent plugin")
	}
}

func TestCapability(t *testing.T) {
	caps := []Capability{
		CapabilityTool,
		CapabilityLLM,
		CapabilityStorage,
		CapabilitySecurity,
		CapabilityUI,
	}

	if len(caps) != 5 {
		t.Errorf("expected 5 capabilities, got %d", len(caps))
	}
}
