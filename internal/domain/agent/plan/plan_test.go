package plan

import (
	"strings"
	"testing"
)

func TestPlanReview(t *testing.T) {
	p := BuildRulePlan("先探索然后修改文件并且验证")
	if p == nil {
		t.Fatal("expected plan")
	}
	pass, gaps := p.Review()
	if pass || len(gaps) == 0 {
		t.Fatal("expected incomplete")
	}
	p.Advance(true, "ok")
	p.Advance(true, "ok")
	p.Advance(true, "ok")
	p.Advance(true, "ok")
	pass, _ = p.Review()
	if !pass {
		t.Fatal("expected pass")
	}
}

func TestPlanView(t *testing.T) {
	p := BuildRulePlan("先探索然后修改文件并且验证")
	if p == nil || len(p.Steps) == 0 {
		t.Fatal("expected plan")
	}
	p.Source = "spec.md"
	p.SpecRef = "spec.md"
	p.Advance(true, "ok")
	v := p.View()
	if v == nil {
		t.Fatal("expected view")
	}
	if v.Goal != p.Goal || v.Source != "spec.md" || v.SpecRef != "spec.md" {
		t.Fatalf("view meta mismatch: %+v", v)
	}
	if v.Total != len(p.Steps) {
		t.Fatalf("total mismatch: %d vs %d", v.Total, len(p.Steps))
	}
	if v.Done != 1 {
		t.Fatalf("done mismatch: %d", v.Done)
	}
	if v.Current != 2 {
		t.Fatalf("current should point to next pending step, got %d", v.Current)
	}
	if v.Progress <= 0 || v.Progress >= 100 {
		t.Fatalf("progress out of range: %v", v.Progress)
	}
	if len(v.Steps) != len(p.Steps) {
		t.Fatalf("steps length mismatch")
	}
	// mark all done, current should reset to 0
	for i := 1; i < len(p.Steps); i++ {
		p.Advance(true, "ok")
	}
	v2 := p.View()
	if v2.Progress != 100 || v2.Done != len(p.Steps) {
		t.Fatalf("expected complete: %+v", v2)
	}
	if v2.Current != 0 {
		t.Fatalf("current should be 0 when done, got %d", v2.Current)
	}
}

func TestPlanVisualize(t *testing.T) {
	p := BuildRulePlan("先探索然后修改文件并且验证")
	if p == nil {
		t.Fatal("expected plan")
	}
	out := p.Visualize()
	if out == "" {
		t.Fatal("expected non-empty visualize")
	}
	if !strings.Contains(out, "Plan:") || !strings.Contains(out, "[ ]") {
		t.Fatalf("visualize missing markers:\n%s", out)
	}
}

func TestPlanReplan(t *testing.T) {
	p := BuildRulePlan("先探索然后修改文件并且验证")
	if p == nil {
		t.Fatal("expected plan")
	}
	np := p.Replan("")
	if np == nil {
		t.Fatal("expected replan")
	}
	if np.Goal != p.Goal {
		t.Fatalf("goal mismatch: %s vs %s", np.Goal, p.Goal)
	}
	if np.Source != "replan" {
		t.Fatalf("source should be replan, got %s", np.Source)
	}
	if np.SpecRef != p.SpecRef {
		t.Fatalf("spec ref should carry over")
	}
	// replan with new goal
	np2 := p.Replan("重构整个模块并且运行测试")
	if np2.Goal != "重构整个模块并且运行测试" {
		t.Fatalf("new goal mismatch: %s", np2.Goal)
	}
}
