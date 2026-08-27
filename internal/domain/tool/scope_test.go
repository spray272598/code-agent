package tool

import (
	"context"
	"testing"
)

func TestScopeChainInheritance(t *testing.T) {
	root := NewScope()
	parent := root.Child()
	child := parent.Child()

	root.Register(&mockTool{name: "bash", desc: "shell"})
	root.Register(&mockTool{name: "read_file", desc: "read"})
	parent.Register(&mockTool{name: "grep", desc: "search"})
	child.Register(&mockTool{name: "bash", desc: "isolated shell"})

	if got := child.Get("bash"); got == nil || got.Name() != "bash" {
		t.Error("child should see bash")
	}
	if got := child.Get("grep"); got == nil || got.Name() != "grep" {
		t.Error("child should see grep from parent")
	}
	if got := child.Get("read_file"); got == nil || got.Name() != "read_file" {
		t.Error("child should see read_file from root")
	}
	if got := child.Get("bash"); got.Description() != "isolated shell" {
		t.Errorf("child's bash should be overridden, got %q", got.Description())
	}
}

func TestScopeRestrict(t *testing.T) {
	scope := NewScope()
	scope.Register(&mockTool{name: "bash", desc: "shell"})
	scope.Register(&mockTool{name: "read_file", desc: "read"})
	scope.Register(&mockTool{name: "write_file", desc: "write"})

	scope.Restrict([]string{"bash", "read_file"})

	if got := scope.Get("bash"); got == nil {
		t.Error("bash should be allowed")
	}
	if got := scope.Get("write_file"); got != nil {
		t.Error("write_file should be restricted")
	}
	list := scope.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tools after restrict, got %d", len(list))
	}

	scope.Restrict(nil)
	if got := scope.Get("write_file"); got == nil {
		t.Error("write_file should be visible after clearing restrict")
	}
}

func TestScopeDeny(t *testing.T) {
	scope := NewScope()
	scope.Register(&mockTool{name: "bash", desc: "shell"})
	scope.Register(&mockTool{name: "read_file", desc: "read"})

	scope.Deny([]string{"bash"})
	if got := scope.Get("bash"); got != nil {
		t.Error("bash should be denied")
	}
	if got := scope.Get("read_file"); got == nil {
		t.Error("read_file should be visible")
	}
}

func TestScopeRestrictAndDeny(t *testing.T) {
	scope := NewScope()
	scope.Register(&mockTool{name: "bash", desc: "shell"})
	scope.Register(&mockTool{name: "read_file", desc: "read"})
	scope.Register(&mockTool{name: "grep", desc: "search"})

	scope.Restrict([]string{"bash", "read_file"})
	scope.Deny([]string{"bash"})

	if got := scope.Get("bash"); got != nil {
		t.Error("bash should be denied even though allowed")
	}
	if got := scope.Get("read_file"); got == nil {
		t.Error("read_file should be visible")
	}
	if got := scope.Get("grep"); got != nil {
		t.Error("grep should be restricted")
	}
}

func TestScopeChainNoParent(t *testing.T) {
	scope := NewScope()
	if got := scope.Get("nonexistent"); got != nil {
		t.Error("should return nil for missing tool")
	}
	list := scope.List()
	if len(list) != 0 {
		t.Error("empty scope should return empty list")
	}
}

type mockTool struct {
	name string
	desc string
}

func (m *mockTool) Name() string                { return m.name }
func (m *mockTool) Description() string         { return m.desc }
func (m *mockTool) InputSchema() map[string]any { return nil }
func (m *mockTool) Execute(_ context.Context, _ map[string]any) (Result, error) {
	return Result{Text: "ok"}, nil
}
