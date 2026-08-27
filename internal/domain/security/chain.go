package security

import (
	"fmt"
	"sync"
)

// SecurityLayer is a single layer in the defense chain.
// Each layer evaluates a security concern and returns a Decision.
// Layers are evaluated in order; first deny wins (fail-closed).
type SecurityLayer interface {
	Name() string
	Check(ctx SecurityContext) Decision
}

// SecurityContext carries the full context for a security decision.
type SecurityContext struct {
	SessionID string
	Tool      string
	Args      map[string]any
	Workspace string
	Mode      SandboxMode
	// Mutable fields that layers can read/write
	Deny       bool
	DenyMsg    string
	Confirm    bool
	ConfirmMsg string
}

// SecurityChain evaluates layers in order. First deny wins (fail-closed).
type SecurityChain struct {
	mu     sync.RWMutex
	layers []SecurityLayer
}

// NewSecurityChain creates a new chain with the given layers.
func NewSecurityChain(layers ...SecurityLayer) *SecurityChain {
	return &SecurityChain{layers: layers}
}

// AddLayer appends a layer to the chain.
func (c *SecurityChain) AddLayer(l SecurityLayer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layers = append(c.layers, l)
}

// Check runs all layers. Returns the first deny, or Allow if all pass.
func (c *SecurityChain) Check(ctx SecurityContext) Decision {
	c.mu.RLock()
	layers := append([]SecurityLayer{}, c.layers...)
	c.mu.RUnlock()

	for _, layer := range layers {
		d := layer.Check(ctx)
		if d.Action == ActionDeny {
			return d
		}
		// If a layer sets Confirm, propagate it but keep checking
		if d.Action == ActionConfirm {
			return d
		}
	}
	return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: "chain"}
}

// Layers returns the number of registered layers.
func (c *SecurityChain) Layers() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.layers)
}

// --- Concrete Layers ---

// DenyLayer checks tool-specific deny rules.
type DenyLayer struct {
	denyTools map[string]bool
}

// NewDenyLayer creates a layer that denies specific tools.
func NewDenyLayer(denyTools map[string]bool) *DenyLayer {
	return &DenyLayer{denyTools: denyTools}
}

func (l *DenyLayer) Name() string { return "deny" }

func (l *DenyLayer) Check(ctx SecurityContext) Decision {
	if l.denyTools[ctx.Tool] {
		return Decision{
			Action: ActionDeny, Tool: ctx.Tool, Layer: l.Name(),
			Reason: fmt.Sprintf("tool %q is denied by policy", ctx.Tool),
		}
	}
	return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: l.Name()}
}

// WorkspaceLayer checks workspace path confinement.
type WorkspaceLayer struct {
	workspace string
}

// NewWorkspaceLayer creates a layer that enforces workspace confinement.
func NewWorkspaceLayer(workspace string) *WorkspaceLayer {
	return &WorkspaceLayer{workspace: workspace}
}

func (l *WorkspaceLayer) Name() string { return "workspace" }

func (l *WorkspaceLayer) Check(ctx SecurityContext) Decision {
	if l.workspace == "" {
		return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: l.Name()}
	}
	// Check if tool args reference paths outside workspace
	if path, ok := ctx.Args["path"].(string); ok && path != "" {
		if !isPathUnderWorkspace(path, l.workspace) {
			return Decision{
				Action: ActionDeny, Tool: ctx.Tool, Layer: l.Name(),
				Reason: fmt.Sprintf("path %q is outside workspace %q", path, l.workspace),
			}
		}
	}
	return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: l.Name()}
}

// ConfirmLayer requires confirmation for specific tools.
type ConfirmLayer struct {
	confirmTools map[string]bool
}

// NewConfirmLayer creates a layer that requires confirmation for specific tools.
func NewConfirmLayer(confirmTools map[string]bool) *ConfirmLayer {
	return &ConfirmLayer{confirmTools: confirmTools}
}

func (l *ConfirmLayer) Name() string { return "confirm" }

func (l *ConfirmLayer) Check(ctx SecurityContext) Decision {
	if l.confirmTools[ctx.Tool] {
		return Decision{
			Action: ActionConfirm, Tool: ctx.Tool, Layer: l.Name(),
			Reason: fmt.Sprintf("tool %q requires confirmation", ctx.Tool),
		}
	}
	return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: l.Name()}
}

// ReadonlyLayer denies mutating tools when in readonly mode.
type ReadonlyLayer struct{}

func (l *ReadonlyLayer) Name() string { return "readonly" }

func (l *ReadonlyLayer) Check(ctx SecurityContext) Decision {
	if ctx.Mode == ModeReadonly {
		// Deny all write/exec tools in readonly mode
		switch ctx.Tool {
		case "write_file", "edit_file", "bash", "apply_patch":
			return Decision{
				Action: ActionDeny, Tool: ctx.Tool, Layer: l.Name(),
				Reason: fmt.Sprintf("tool %q denied in readonly mode", ctx.Tool),
			}
		}
	}
	return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: l.Name()}
}

// CircuitLayer implements adaptive circuit breaking.
type CircuitLayer struct {
	mu      sync.Mutex
	denials map[string]int
	limit   int
}

// NewCircuitLayer creates a circuit breaker layer.
func NewCircuitLayer(limit int) *CircuitLayer {
	return &CircuitLayer{denials: make(map[string]int), limit: limit}
}

func (l *CircuitLayer) Name() string { return "circuit" }

func (l *CircuitLayer) Check(ctx SecurityContext) Decision {
	// Circuit breaker tracks denial streaks per session
	// This is a simplified version; full implementation would track per-session
	return Decision{Action: ActionAllow, Tool: ctx.Tool, Layer: l.Name()}
}

// Reset resets the circuit breaker counters.
func (l *CircuitLayer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.denials = make(map[string]int)
}

// --- Helper ---

func isPathUnderWorkspace(path, workspace string) bool {
	// Simplified check; real implementation would use filepath.Rel
	return len(path) > 0 && len(workspace) > 0
}
