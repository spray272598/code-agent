package security

import (
	"testing"
)

// TestNewGuardModePopulatesSubStructs verifies the Guard constructor wires the
// split state/config/components sub-structs instead of a flat field set.
func TestNewGuardModePopulatesSubStructs(t *testing.T) {
	g := NewGuardMode("/workspace", true, true, ModeStrict)
	if g == nil {
		t.Fatal("NewGuardMode returned nil")
	}

	// guardConfig
	if g.config.workspace != "/workspace" {
		t.Errorf("config.workspace = %q, want /workspace", g.config.workspace)
	}
	if !g.config.pathSandbox {
		t.Error("config.pathSandbox = false, want true")
	}
	if !g.config.confirmWrite {
		t.Error("config.confirmWrite = false, want true")
	}
	if g.config.circuitLimit != 5 {
		t.Errorf("config.circuitLimit = %d, want 5", g.config.circuitLimit)
	}
	if g.config.mode != ModeStrict {
		t.Errorf("config.mode = %v, want ModeStrict", g.config.mode)
	}
	if !g.config.mcpConfirm {
		t.Error("config.mcpConfirm = false, want true")
	}
	if g.config.readTools["read_file"] != true {
		t.Error("config.readTools missing read_file")
	}
	if g.config.writeTools["write_file"] != true {
		t.Error("config.writeTools missing write_file")
	}
	if len(g.config.denyRules) == 0 {
		t.Error("config.denyRules not initialized")
	}
	if len(g.config.confirmRules) == 0 {
		t.Error("config.confirmRules not initialized")
	}

	// guardState (mutable maps must be allocated)
	if g.state.sessionAllow == nil || g.state.pending == nil ||
		g.state.awaiting == nil || g.state.denyStreak == nil || g.state.sessionWS == nil {
		t.Error("guardState maps were not allocated")
	}

	// guardComponents (extended security subsystems wired up)
	if g.DenyEngine() == nil {
		t.Error("DenyEngine() is nil")
	}
	if g.Sanitizer() == nil {
		t.Error("Sanitizer() is nil")
	}
	if g.Audit() == nil {
		t.Error("Audit() is nil")
	}
	if g.NetworkEnforcer() == nil {
		t.Error("NetworkEnforcer() is nil")
	}
	if g.SandboxManager() == nil {
		t.Error("SandboxManager() is nil")
	}
	if g.InjectionDetector() == nil {
		t.Error("InjectionDetector() is nil")
	}
	if g.BehaviorTracker() == nil {
		t.Error("BehaviorTracker() is nil")
	}
	if g.IntegrityChain() == nil {
		t.Error("IntegrityChain() is nil")
	}
	if g.AdaptiveBreaker() == nil {
		t.Error("AdaptiveBreaker() is nil")
	}
}

// TestGuardModeAccessorAndSetter confirms Mode()/SetMode operate on the
// config sub-struct and are visible through the public accessor.
func TestGuardModeAccessorAndSetter(t *testing.T) {
	g := NewGuard("/ws", true, true)
	if g.Mode() != ModeWorkspace {
		t.Fatalf("initial mode = %v, want ModeWorkspace", g.Mode())
	}
	g.SetMode(ModeReadonly)
	if g.config.mode != ModeReadonly {
		t.Errorf("after SetMode, config.mode = %v, want ModeReadonly", g.config.mode)
	}
	if g.Mode() != ModeReadonly {
		t.Errorf("Mode() = %v, want ModeReadonly", g.Mode())
	}
}

// TestGuardSessionApprovalUsesState verifies the pending/approval flow stores
// state in the guardState sub-struct.
func TestGuardSessionApprovalUsesState(t *testing.T) {
	g := NewGuard("/ws", true, true)
	d := Decision{Action: ActionConfirm, Tool: "bash", Reason: "shell"}
	p := g.CreatePending("sess1", "bash", map[string]any{"command": "ls"}, d)
	if g.state.pending[p.ID] == nil {
		t.Fatal("pending not recorded in guardState.pending")
	}
	if g.state.awaiting["sess1"] == nil {
		t.Fatal("awaiting not recorded in guardState.awaiting")
	}

	if _, err := g.Approve(p.ID, "session"); err != nil {
		t.Fatalf("Approve error: %v", err)
	}
	if g.state.sessionAllow["sess1"]["*"] != true {
		t.Error("session-wide approval not stored in guardState.sessionAllow")
	}
	if g.state.pending[p.ID] != nil {
		t.Error("pending should be cleared after approve")
	}
	if g.state.denyStreak["sess1"] != 0 {
		t.Error("deny streak should reset on approve")
	}

	// A session-approved session should allow bash without confirm.
	dec := g.Check("sess1", "bash", map[string]any{"command": "ls"})
	if dec.Action != ActionAllow {
		t.Errorf("Check after session approve = %v, want ActionAllow", dec.Action)
	}
}

// TestGuardRejectIncrementsStreak confirms Reject bumps the streak in guardState.
func TestGuardRejectIncrementsStreak(t *testing.T) {
	g := NewGuard("/ws", true, true)
	d := Decision{Action: ActionConfirm, Tool: "bash", Reason: "shell"}
	p := g.CreatePending("sess2", "bash", map[string]any{"command": "ls"}, d)
	if err := g.Reject(p.ID); err != nil {
		t.Fatalf("Reject error: %v", err)
	}
	if g.state.denyStreak["sess2"] != 1 {
		t.Errorf("denyStreak = %d, want 1", g.state.denyStreak["sess2"])
	}
	if g.state.awaiting["sess2"] != nil {
		t.Error("awaiting should be cleared after reject")
	}
}

// TestGuardReadonlyDeniesMutating verifies the sandbox tier in guardConfig drives
// the L1 readonly decision.
func TestGuardReadonlyDeniesMutating(t *testing.T) {
	g := NewGuardMode("/ws", true, true, ModeReadonly)
	dec := g.Check("s", "write_file", map[string]any{"path": "a.txt"})
	if dec.Action != ActionDeny {
		t.Errorf("readonly write_file = %v, want ActionDeny", dec.Action)
	}
	if dec.Layer != "L1" {
		t.Errorf("readonly layer = %q, want L1", dec.Layer)
	}
}
