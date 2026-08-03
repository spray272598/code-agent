package security

import "testing"

func TestPathSandbox(t *testing.T) {
	g := NewGuard("./workspace", true, true)
	// write outside should deny when path is absolute escape - relative ../
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
