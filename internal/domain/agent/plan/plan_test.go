package plan

import "testing"

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
