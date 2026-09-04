package security

import (
	"testing"
)

// TestOSLevelSandboxEnforcementLevel verifies the facade reports a truthful
// enforcement level instead of always claiming "active". Every platform stub now
// returns at least LevelHeuristic (in-process screening) after a successful
// ApplyProfile, never LevelNone.
func TestOSLevelSandboxEnforcementLevel(t *testing.T) {
	sb := NewOSLevelSandbox(nil, nil)
	if err := sb.ApplyProfile(ProfileConfig{
		Deny: []string{"**/.env"},
	}, "/workspace"); err != nil {
		t.Fatalf("ApplyProfile: %v", err)
	}

	lvl := sb.EnforcementLevel()
	switch lvl {
	case LevelNone:
		t.Fatalf("EnforcementLevel = none after ApplyProfile; sandbox should at least be heuristic")
	case LevelHeuristic, LevelKernel:
		// expected
	default:
		t.Fatalf("unexpected EnforcementLevel %v", lvl)
	}

	if !sb.IsActive() {
		t.Errorf("IsActive() = false for level %v; heuristic still enforces path/network screening", lvl)
	}
}

func TestEnforcementLevelString(t *testing.T) {
	cases := map[EnforcementLevel]string{
		LevelNone:     "none",
		LevelHeuristic: "heuristic",
		LevelKernel:   "kernel",
	}
	for lvl, want := range cases {
		if got := lvl.String(); got != want {
			t.Errorf("EnforcementLevel(%d).String() = %q, want %q", lvl, got, want)
		}
	}
}
