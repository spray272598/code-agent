package tool

import (
	"context"
	"testing"
)

func TestCodeModeBasic(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "bash", desc: "shell"})
	reg.Register(&mockTool{name: "grep", desc: "search"})

	cm := NewCodeModeTool(reg)
	scriptTool := cm.ScriptTool()

	if scriptTool.Name() != "run_code" {
		t.Errorf("expected run_code, got %s", scriptTool.Name())
	}

	// Execute a code script
	res, err := scriptTool.Execute(context.Background(), map[string]any{
		"code": `bash({"command": "echo hello"})`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %s", res.Text)
	}
	if res.Text == "" {
		t.Error("expected non-empty result")
	}
}

func TestCodeModeToolNotFound(t *testing.T) {
	reg := NewRegistry()
	cm := NewCodeModeTool(reg)
	scriptTool := cm.ScriptTool()

	res, _ := scriptTool.Execute(context.Background(), map[string]any{
		"code": `nonexistent({})`,
	})
	if !res.IsError && res.Text == "" {
		t.Error("expected error or message for missing tool")
	}
}

func TestCodeModeEmptyCode(t *testing.T) {
	cm := NewCodeModeTool(NewRegistry())
	res, err := cm.ScriptTool().Execute(context.Background(), map[string]any{"code": ""})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for empty code")
	}
}

func TestCodeModeNoInvocations(t *testing.T) {
	cm := NewCodeModeTool(NewRegistry())
	res, _ := cm.ScriptTool().Execute(context.Background(), map[string]any{
		"code": "# just a comment\n// another comment",
	})
	if res.Text != "no tool invocations found in code" {
		t.Errorf("unexpected result: %s", res.Text)
	}
}

func TestCodeModeSDKPrompt(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "bash", desc: "Run shell commands"})
	cm := NewCodeModeTool(reg)
	prompt := cm.SDKPrompt()
	if prompt == "" {
		t.Error("expected non-empty SDK prompt")
	}
	if !containsStr(prompt, "bash") {
		t.Error("SDK prompt should mention bash")
	}
}

func TestParseCodeInvocations(t *testing.T) {
	code := `
# comment
bash({"command": "ls"})
read_file({"path": "main.go"})
`
	inv := parseCodeInvocations(code)
	if len(inv) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(inv))
	}
	if inv[0].Name != "bash" || inv[1].Name != "read_file" {
		t.Errorf("unexpected names: %v", inv)
	}
}

func TestParseSingleInvocation(t *testing.T) {
	// Valid
	inv := parseSingleInvocation(`bash({"command": "echo hi"})`)
	if inv == nil || inv.Name != "bash" || inv.Args["command"] != "echo hi" {
		t.Errorf("unexpected: %v", inv)
	}
	// No args
	inv = parseSingleInvocation(`grep({})`)
	if inv == nil || inv.Name != "grep" {
		t.Errorf("unexpected: %v", inv)
	}
	// Invalid JSON
	inv = parseSingleInvocation(`bash({invalid})`)
	if inv != nil {
		t.Error("should return nil for invalid JSON")
	}
	// No parentheses
	inv = parseSingleInvocation(`bash`)
	if inv != nil {
		t.Error("should return nil for no parentheses")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}())
}
