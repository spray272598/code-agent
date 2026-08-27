package einoorch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

func TestPromptBuilderDynamic(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	ctx := NewPromptContext()
	pb := NewPromptBuilder(ctx, reg)
	sys := pb.Build(context.Background(), "u", "p", "hello", nil, 8000)
	if !strings.Contains(sys, "Code-Agent") {
		t.Fatal("missing header/system label")
	}
	if !strings.Contains(sys, "echo") {
		t.Fatal("missing tools")
	}
	if !strings.Contains(sys, "Token budget") {
		t.Fatal("missing budget")
	}
	if !strings.Contains(sys, "work_policy") {
		t.Fatal("missing work_policy section")
	}
	if !strings.Contains(sys, "tool_calling") {
		t.Fatal("missing tool_calling section")
	}
	if !strings.Contains(sys, "communication") {
		t.Fatal("missing communication section")
	}
	if !strings.Contains(sys, "formatting") {
		t.Fatal("missing formatting section")
	}
	if !strings.Contains(sys, "user_info") {
		t.Fatal("missing user_info section")
	}
	if !strings.Contains(sys, "delegation_guidance") {
		t.Fatal("missing delegation_guidance section")
	}
	sys2 := pb.Build(context.Background(), "u", "p", "hello2", nil, 8000)
	if !strings.Contains(sys2, "echo") {
		t.Fatal("cache path broken")
	}
}

func TestPromptBuilderToolCatalog(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	reg.Register(readT{})
	reg.Register(editT{})
	reg.Register(grepT{})
	ctx := NewPromptContext()
	pb := NewPromptBuilder(ctx, reg)
	sys := pb.Build(context.Background(), "", "", "", nil, 8000)

	if !strings.Contains(sys, "work_policy") {
		t.Fatal("should have work_policy for primary audience")
	}
	if !strings.Contains(sys, "read_file") || !strings.Contains(sys, "edit_file") {
		t.Fatal("tool names not resolved in prompt")
	}
	if !strings.Contains(sys, "code_changes") {
		t.Fatal("missing code_change_rules when edit tool present")
	}
	if !strings.Contains(sys, "testing_discipline") {
		t.Fatal("missing testing_discipline when edit tool present")
	}
}

func TestPromptBuilderSubagentMode(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	ctx := NewPromptContext()
	ctx.Audience = AudienceSubagent
	pb := NewPromptBuilder(ctx, reg)
	sys := pb.Build(context.Background(), "", "", "", nil, 4000)

	if strings.Contains(sys, "work_policy") {
		t.Fatal("subagent prompt should not have work_policy")
	}
	if strings.Contains(sys, "tool_calling") {
		t.Fatal("subagent prompt should not have tool_calling section")
	}
	if !strings.Contains(sys, "user_info") {
		t.Fatal("subagent prompt should still have user_info")
	}
}

func TestPromptBuilderSkill(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	ctx := NewPromptContext()
	pb := NewPromptBuilder(ctx, reg)
	skSvc := skill.NewService(t.TempDir())
	pb.SetSkills(skSvc)
	sk := &skill.Skill{ID: "s1", Name: "S1", Body: "do explore", Tools: []string{"echo"}}
	sys := pb.Build(context.Background(), "", "", "x", sk, 1000)
	if !strings.Contains(sys, "S1") && !strings.Contains(sys, "Active Skill") {
		if !strings.Contains(sys, "do explore") && !strings.Contains(sys, "echo") {
			t.Fatalf("skill not injected: %s", sys[:min(200, len(sys))])
		}
	}
}

func TestPromptBuilderCompact(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	reg.Register(readT{})
	ctx := NewPromptContext()
	pb := NewPromptBuilder(ctx, reg)
	sys := pb.BuildCompact(4000)
	if !strings.Contains(sys, "continuing a previous conversation") {
		t.Fatal("compact prompt should mention continuation")
	}
	if !strings.Contains(sys, "context has been summarized") {
		t.Fatal("compact prompt should mention summary")
	}
	if strings.Contains(sys, "work_policy") {
		t.Fatal("compact prompt should not have full sections")
	}
}

func TestPromptBuilderForSubagent(t *testing.T) {
	reg := tool.NewRegistry()
	ctx := NewPromptContext()
	pb := NewPromptBuilder(ctx, reg)
	sys := pb.BuildForSubagent(RoleExplore, "/workspace/test", []string{"read_file", "grep"}, 20)
	if !strings.Contains(sys, "EXPLORE") {
		t.Fatal("should use explore prompt")
	}
	if !strings.Contains(sys, "read_file") {
		t.Fatal("should list available tools")
	}
	if !strings.Contains(sys, "Maximum steps") {
		t.Fatal("should include step limit")
	}
	if !strings.Contains(sys, "user_info") {
		t.Fatal("should include user_info")
	}
}

func TestPromptContextUserInfo(t *testing.T) {
	ctx := NewPromptContext()
	block := ctx.UserInfoBlock()
	if !strings.Contains(block, "OS: ") {
		t.Fatal("user_info should include OS")
	}
	if !strings.Contains(block, "Shell: ") {
		t.Fatal("user_info should include Shell")
	}
	if !strings.Contains(block, "Workspace Path: ") {
		t.Fatal("user_info should include Workspace Path")
	}
	if !strings.Contains(block, "Current Date: ") {
		t.Fatal("user_info should include Current Date")
	}
}

func TestPromptContextNonInteractive(t *testing.T) {
	ctx := NewPromptContext()
	ctx.IsNonInteractive = true
	header := ctx.Header()
	if !strings.Contains(header, "non-interactive") {
		t.Fatal("non-interactive flag should appear in header")
	}
}

func TestToolCatalogDetection(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(bashT{})
	reg.Register(readT{})
	reg.Register(editT{})
	reg.Register(grepT{})
	catalog := BuildToolCatalog(reg)
	if !catalog.HasRead {
		t.Fatal("should detect read tool")
	}
	if !catalog.HasEdit {
		t.Fatal("should detect edit tool")
	}
	if !catalog.HasSearch {
		t.Fatal("should detect search tool")
	}
	if !catalog.HasExec {
		t.Fatal("should detect exec tool")
	}
}

func TestSubagentPrompts(t *testing.T) {
	prompts := map[string]string{
		RoleGeneralPurpose: GeneralPurposePrompt,
		RoleExplore:        ExplorePrompt,
		RolePlan:           PlanPrompt,
		RoleVerify:         VerifyPrompt,
	}
	for role, expected := range prompts {
		got := SubagentPrompt(role)
		if got != expected {
			t.Fatalf("SubagentPrompt(%s) mismatch", role)
		}
	}
	if SubagentPrompt("unknown") != GeneralPurposePrompt {
		t.Fatal("unknown role should default to general purpose")
	}
}

func TestWorkPolicySection(t *testing.T) {
	section := WorkPolicySection()
	if !strings.Contains(section, "<work_policy>") {
		t.Fatal("should have work_policy tag")
	}
	if !strings.Contains(section, "Keep every explicit requirement") {
		t.Fatal("should contain requirement tracking")
	}
}

func TestCommunicationSection(t *testing.T) {
	section := CommunicationSection()
	if !strings.Contains(section, "<communication>") {
		t.Fatal("should have communication tag")
	}
	if !strings.Contains(section, "Lead with the answer") {
		t.Fatal("should contain lead-with-answer guidance")
	}
}

func TestDelegationGuidanceSection(t *testing.T) {
	section := DelegationGuidanceSection()
	if !strings.Contains(section, "<delegation_guidance>") {
		t.Fatal("should have delegation_guidance tag")
	}
	if !strings.Contains(section, "part of the requested outcome") {
		t.Fatal("should state delegation is part of the outcome")
	}
	if !strings.Contains(section, "self-contained brief") {
		t.Fatal("should instruct a self-contained subagent brief")
	}
}

func TestFormattingSection(t *testing.T) {
	section := FormattingSection()
	if !strings.Contains(section, "<formatting>") {
		t.Fatal("should have formatting tag")
	}
	if !strings.Contains(section, "GitHub-flavored markdown") {
		t.Fatal("should mention markdown")
	}
}

func TestVerifyPrompt(t *testing.T) {
	p := DefaultVerifierPrompt()
	if !strings.Contains(p, "adversarial verifier") {
		t.Fatal("should mention adversarial verifier")
	}
	if !strings.Contains(p, "Anti-ratchet") {
		t.Fatal("should mention anti-ratchet")
	}
	if !strings.Contains(p, "Decision rules") {
		t.Fatal("should mention decision rules")
	}
}

func TestAgentsMdDiscovery(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Test Instructions\nFollow these rules."), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	files := DiscoverAgentsMdFiles(dir, 2)
	if len(files) != 1 {
		t.Fatalf("expected 1 AGENTS.md file, got %d", len(files))
	}
	if !strings.Contains(files[0].Content, "Test Instructions") {
		t.Fatal("content not read")
	}
}

func TestAgentsMdFormatting(t *testing.T) {
	files := []AgentsMdFile{
		{FilePath: "/repo/AGENTS.md", FileName: "AGENTS.md", Content: "# Rules"},
	}
	section := FormatAgentsMdSection(files)
	if !strings.Contains(section, "system-reminder") {
		t.Fatal("should wrap in system-reminder")
	}
	if !strings.Contains(section, "/repo/AGENTS.md") {
		t.Fatal("should include file path")
	}
	if !strings.Contains(section, "# Rules") {
		t.Fatal("should include content")
	}
}

func TestEmptyAgentsMd(t *testing.T) {
	section := FormatAgentsMdSection(nil)
	if section != "" {
		t.Fatal("empty files should produce empty section")
	}
}

func TestMemoryEnabling(t *testing.T) {
	section := MemorySection(true)
	if !strings.Contains(section, "<memory>") {
		t.Fatal("should have memory tag when enabled")
	}
	disabled := MemorySection(false)
	if disabled != "" {
		t.Fatal("should return empty when disabled")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
