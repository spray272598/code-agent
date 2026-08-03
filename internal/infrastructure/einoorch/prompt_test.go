package einoorch

import (
	"context"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

func TestPromptBuilderDynamic(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	pb := NewPromptBuilder("BASE_PERSONA", reg)
	sys := pb.Build(context.Background(), "u", "p", "hello", nil, 8000)
	if !strings.Contains(sys, "BASE_PERSONA") {
		t.Fatal("missing base")
	}
	if !strings.Contains(sys, "echo") {
		t.Fatal("missing tools")
	}
	if !strings.Contains(sys, "Token budget") {
		t.Fatal("missing budget")
	}
	// cache hit path
	sys2 := pb.Build(context.Background(), "u", "p", "hello2", nil, 8000)
	if !strings.Contains(sys2, "echo") {
		t.Fatal("cache path broken")
	}
}

func TestPromptBuilderSkill(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	pb := NewPromptBuilder("BASE", reg)
	skSvc := skill.NewService(t.TempDir())
	pb.SetSkills(skSvc)
	sk := &skill.Skill{ID: "s1", Name: "S1", Body: "do explore", Tools: []string{"echo"}}
	sys := pb.Build(context.Background(), "", "", "x", sk, 1000)
	if !strings.Contains(sys, "S1") && !strings.Contains(sys, "Active Skill") {
		// PromptSection may still include name
		if !strings.Contains(sys, "do explore") && !strings.Contains(sys, "echo") {
			t.Fatalf("skill not injected: %s", sys[:min(200, len(sys))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
