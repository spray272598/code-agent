package security

import "testing"

func TestPathSandbox(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	d := g.Check("s1", "read_file", map[string]any{"path": "../secret"})
	if d.Action != ActionDeny {
		t.Fatalf("want deny for ../, got %s %s", d.Action, d.Reason)
	}
	d2 := g.Check("s1", "read_file", map[string]any{"path": "README.md"})
	if d2.Action != ActionAllow {
		t.Fatalf("want allow, got %s", d2.Action)
	}
}

func TestApproveResume(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	d := g.Check("s1", "write_file", map[string]any{"path": "a.txt", "content": "x"})
	if d.Action != ActionConfirm {
		t.Fatalf("want confirm, got %s", d.Action)
	}
	p := g.CreatePending("s1", "write_file", map[string]any{"path": "a.txt", "content": "x"}, d)
	if _, err := g.Approve(p.ID, "once"); err != nil {
		t.Fatal(err)
	}
	r := g.TakeReadyResume("s1")
	if r == nil || !r.Ready {
		t.Fatal("expected ready resume")
	}
}

func TestDenyBypassAttempts(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	// various obfuscations of rm -rf /
	attacks := []string{
		"rm -rf /",
		"rm  -rf  /",
		"Rm -Rf /",
		"rm%20-rf%20/",
		"rm\t-rf\t/",
		"rm -rf /",
	}
	for _, a := range attacks {
		d := g.Check("s1", "bash", map[string]any{"command": a})
		if d.Action != ActionDeny {
			t.Fatalf("command %q should deny, got %s (%s)", a, d.Action, d.Reason)
		}
	}
}

func TestMCPDefaultConfirm(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	d := g.Check("s1", "demo__delete_everything", map[string]any{"x": "1"})
	if d.Action != ActionConfirm {
		t.Fatalf("mcp unknown should confirm, got %s", d.Action)
	}
	d2 := g.Check("s1", "fs__read_file", map[string]any{"path": "a"})
	// read-like mcp may allow or confirm depending on path sandbox
	if d2.Action == ActionDeny && d2.RuleID != "path_sandbox" {
		t.Fatalf("unexpected deny: %+v", d2)
	}
}

func TestNormalizeCommand(t *testing.T) {
	n := NormalizeCommand("Rm%20-Rf%20/")
	if !contains(n, "rm") || !contains(n, "-rf") {
		t.Fatalf("normalized=%q", n)
	}
}

func TestDenySemicolonVariant(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	// payload after benign prefix via ;
	attacks := []string{
		"echo ok; rm -rf /",
		"true && rm -rf /",
		"rm  -rf  /",
		"rm;-rf /",
	}
	for _, a := range attacks {
		d := g.Check("s1", "bash", map[string]any{"command": a})
		if d.Action != ActionDeny {
			t.Fatalf("command %q should deny, got %s (%s)", a, d.Action, d.Reason)
		}
	}
}

func TestPathURLEncodeBypass(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	// percent-encoded ../
	d := g.Check("s1", "read_file", map[string]any{"path": "%2e%2e/secret"})
	if d.Action != ActionDeny {
		t.Fatalf("url-encoded ../ should deny, got %s", d.Action)
	}
	d2 := g.Check("s1", "read_file", map[string]any{"path": "..%2fsecret"})
	if d2.Action != ActionDeny {
		t.Fatalf("mixed encode should deny, got %s", d2.Action)
	}
}

func TestMCPWriteNotAutoAllow(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	d := g.Check("s1", "demo__write_file", map[string]any{"path": "a"})
	if d.Action == ActionAllow {
		t.Fatalf("mcp write should not auto-allow")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
