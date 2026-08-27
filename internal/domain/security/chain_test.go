package security

import (
	"testing"
)

func TestSecurityChainAllow(t *testing.T) {
	chain := NewSecurityChain(
		NewDenyLayer(map[string]bool{"dangerous_tool": true}),
		NewConfirmLayer(map[string]bool{"write_file": true}),
	)

	d := chain.Check(SecurityContext{Tool: "read_file"})
	if d.Action != ActionAllow {
		t.Errorf("expected allow, got %s", d.Action)
	}
}

func TestSecurityChainDeny(t *testing.T) {
	chain := NewSecurityChain(
		NewDenyLayer(map[string]bool{"dangerous_tool": true}),
		NewConfirmLayer(map[string]bool{"write_file": true}),
	)

	d := chain.Check(SecurityContext{Tool: "dangerous_tool"})
	if d.Action != ActionDeny {
		t.Errorf("expected deny, got %s", d.Action)
	}
	if d.Layer != "deny" {
		t.Errorf("expected layer deny, got %s", d.Layer)
	}
}

func TestSecurityChainConfirm(t *testing.T) {
	chain := NewSecurityChain(
		NewDenyLayer(map[string]bool{}),
		NewConfirmLayer(map[string]bool{"write_file": true}),
	)

	d := chain.Check(SecurityContext{Tool: "write_file"})
	if d.Action != ActionConfirm {
		t.Errorf("expected confirm, got %s", d.Action)
	}
	if d.Layer != "confirm" {
		t.Errorf("expected layer confirm, got %s", d.Layer)
	}
}

func TestSecurityChainFirstDenyWins(t *testing.T) {
	chain := NewSecurityChain(
		NewDenyLayer(map[string]bool{"tool_a": true}),
		NewDenyLayer(map[string]bool{"tool_a": true}), // second layer also denies
	)

	d := chain.Check(SecurityContext{Tool: "tool_a"})
	if d.Action != ActionDeny {
		t.Errorf("expected deny, got %s", d.Action)
	}
	if d.Layer != "deny" {
		t.Errorf("expected first deny layer, got %s", d.Layer)
	}
}

func TestSecurityChainAddLayer(t *testing.T) {
	chain := NewSecurityChain()
	if chain.Layers() != 0 {
		t.Errorf("expected 0 layers, got %d", chain.Layers())
	}

	chain.AddLayer(NewDenyLayer(map[string]bool{}))
	if chain.Layers() != 1 {
		t.Errorf("expected 1 layer, got %d", chain.Layers())
	}
}

func TestSecurityChainEmpty(t *testing.T) {
	chain := NewSecurityChain()
	d := chain.Check(SecurityContext{Tool: "any_tool"})
	if d.Action != ActionAllow {
		t.Errorf("expected allow for empty chain, got %s", d.Action)
	}
}

func TestReadonlyLayer(t *testing.T) {
	l := &ReadonlyLayer{}

	d := l.Check(SecurityContext{Tool: "write_file", Mode: ModeReadonly})
	if d.Action != ActionDeny {
		t.Errorf("expected deny for write in readonly, got %s", d.Action)
	}

	d = l.Check(SecurityContext{Tool: "read_file", Mode: ModeReadonly})
	if d.Action != ActionAllow {
		t.Errorf("expected allow for read in readonly, got %s", d.Action)
	}

	d = l.Check(SecurityContext{Tool: "write_file", Mode: ModeWorkspace})
	if d.Action != ActionAllow {
		t.Errorf("expected allow for write in workspace, got %s", d.Action)
	}
}

func TestSecurityChainCustomLayer(t *testing.T) {
	called := false
	custom := &testLayer{
		name: "custom",
		fn: func(ctx SecurityContext) Decision {
			called = true
			if ctx.Args["block"] == true {
				return Decision{Action: ActionDeny, Tool: ctx.Tool, Layer: "custom"}
			}
			return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: "custom"}
		},
	}

	chain := NewSecurityChain(custom)
	d := chain.Check(SecurityContext{Tool: "test", Args: map[string]any{"block": true}})
	if d.Action != ActionDeny {
		t.Errorf("expected deny, got %s", d.Action)
	}
	if !called {
		t.Error("custom layer not called")
	}
}

type testLayer struct {
	name string
	fn   func(SecurityContext) Decision
}

func (l *testLayer) Name() string                       { return l.name }
func (l *testLayer) Check(ctx SecurityContext) Decision { return l.fn(ctx) }
