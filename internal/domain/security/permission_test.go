package security

import "testing"

func TestSandboxModeReadonly(t *testing.T) {
	g := NewGuardMode("./workspace", true, true, ModeReadonly)
	d := g.Check("s1", "write_file", map[string]any{"path": "a.txt", "content": "x"})
	if d.Action != ActionDeny {
		t.Fatalf("readonly: want deny write, got %s", d.Action)
	}
	d2 := g.Check("s1", "bash", map[string]any{"command": "echo hi"})
	if d2.Action != ActionDeny {
		t.Fatalf("readonly: want deny bash, got %s", d2.Action)
	}
	// read tools still allowed
	if r := g.Check("s1", "read_file", map[string]any{"path": "a.txt"}); r.Action != ActionAllow {
		t.Fatalf("readonly: read should allow, got %s", r.Action)
	}
	// runtime switch back to workspace re-enables writes
	g.SetMode(ModeWorkspace)
	if r := g.Check("s1", "write_file", map[string]any{"path": "a.txt", "content": "x"}); r.Action != ActionConfirm {
		t.Fatalf("after SetMode: want confirm, got %s", r.Action)
	}
}

func TestSandboxModeStrict(t *testing.T) {
	g := NewGuardMode("./workspace", true, true, ModeStrict)
	// mutating write tool still confirmable (path sandbox handles location)
	if r := g.Check("s1", "write_file", map[string]any{"path": "a.txt"}); r.Action != ActionConfirm {
		t.Fatalf("strict: write should confirm, got %s", r.Action)
	}
	// network egress blocked
	if r := g.Check("s1", "bash", map[string]any{"command": "curl https://evil"}); r.Action != ActionDeny {
		t.Fatalf("strict: egress should deny, got %s", r.Action)
	}
	if r := g.Check("s1", "bash", map[string]any{"command": "ssh host"}); r.Action != ActionDeny {
		t.Fatalf("strict: ssh should deny, got %s", r.Action)
	}
}

func TestParseSandboxMode(t *testing.T) {
	if ParseSandboxMode("readonly") != ModeReadonly {
		t.Fatal("readonly parse")
	}
	if ParseSandboxMode("read-only") != ModeReadonly {
		t.Fatal("read-only parse")
	}
	if ParseSandboxMode("ro") != ModeReadonly {
		t.Fatal("ro parse")
	}
	if ParseSandboxMode("STRICT") != ModeStrict {
		t.Fatal("strict parse")
	}
	if ParseSandboxMode("") != ModeWorkspace {
		t.Fatal("empty -> workspace")
	}
	if ModeReadonly.String() != "readonly" || ModeStrict.String() != "strict" || ModeWorkspace.String() != "workspace" {
		t.Fatal("mode string")
	}
}

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
