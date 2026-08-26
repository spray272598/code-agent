package skill

import (
	"context"
	"os"
	"testing"
)

func osMkdirAll(path string) error   { return os.MkdirAll(path, 0o755) }
func osWriteFile(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

func TestSubstituteArgumentsFull(t *testing.T) {
	body := "Run: $ARGUMENTS"
	out := Substitute(body, "fix typo", SubstitutionContext{})
	if out != "Run: fix typo" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteIndexedArguments(t *testing.T) {
	body := "File: $ARGUMENTS[0], Action: $ARGUMENTS[1]"
	out := Substitute(body, "main.go refactor", SubstitutionContext{})
	if out != "File: main.go, Action: refactor" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteShorthandN(t *testing.T) {
	body := "Commit with message: $0"
	out := Substitute(body, "fix bug", SubstitutionContext{})
	if out != "Commit with message: fix" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteSkillDir(t *testing.T) {
	body := "Config at ${SKILL_DIR}/config.json"
	out := Substitute(body, "", SubstitutionContext{SkillDir: "/home/user/.grok/skills/deploy"})
	if out != "Config at /home/user/.grok/skills/deploy/config.json" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteSessionID(t *testing.T) {
	body := "Session: ${SESSION_ID}"
	out := Substitute(body, "", SubstitutionContext{SessionID: "abc-123"})
	if out != "Session: abc-123" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteNoTokenAppendsSuffix(t *testing.T) {
	body := "# Commit\n\nDo the commit."
	out := Substitute(body, "fix bug", SubstitutionContext{})
	want := "# Commit\n\nDo the commit.\n\n**ARGUMENTS:** fix bug"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteNoArgsUnchanged(t *testing.T) {
	body := "# Commit\n\nDo the commit."
	out := Substitute(body, "", SubstitutionContext{})
	if out != body {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteRealArgSuppressesSuffix(t *testing.T) {
	body := "Run: $ARGUMENTS (cost: $100)"
	out := Substitute(body, "deploy", SubstitutionContext{})
	if out != "Run: deploy (cost: $100)" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteDollarAmountDoesNotSuppressSuffix(t *testing.T) {
	body := "Price: $100 per unit."
	out := Substitute(body, "deploy staging", SubstitutionContext{})
	want := "Price: $100 per unit.\n\n**ARGUMENTS:** deploy staging"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestSubstitutePluginTokens(t *testing.T) {
	body := "Root ${PLUGIN_ROOT}, data ${PLUGIN_DATA}"
	out := Substitute(body, "", SubstitutionContext{PluginRoot: "/plugins/vdc", PluginData: "/data/vdc"})
	if out != "Root /plugins/vdc, data /data/vdc" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteUserAndWorkspace(t *testing.T) {
	body := "User ${USER_ID} in ${WORKSPACE}"
	out := Substitute(body, "", SubstitutionContext{UserID: "u-1", Workspace: "/ws"})
	if out != "User u-1 in /ws" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteMissingIndex(t *testing.T) {
	body := "Arg 0: $0, Arg 5: $5"
	out := Substitute(body, "only-one", SubstitutionContext{})
	if out != "Arg 0: only-one, Arg 5: " {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteEmpty(t *testing.T) {
	if out := Substitute("", "x", SubstitutionContext{}); out != "" {
		t.Fatalf("got %q", out)
	}
}

func TestSkillInfo(t *testing.T) {
	sk := &Skill{Name: "commit", Description: "Create a git commit", Path: "/skills/commit/SKILL.md", Body: "body"}
	got := SkillInfo(sk)
	if got == "" {
		t.Fatal("empty")
	}
}

func TestSkillsBlockEmpty(t *testing.T) {
	if got := SkillsBlock(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestComposeCycleDetection(t *testing.T) {
	svc := NewService(t.TempDir())
	// Manually construct circular skills
	svc.skills = map[string]*Skill{
		"a": {ID: "a", Name: "a", Body: "A", Depends: []string{"b"}, Enabled: true},
		"b": {ID: "b", Name: "b", Body: "B", Depends: []string{"c"}, Enabled: true},
		"c": {ID: "c", Name: "c", Body: "C", Depends: []string{"a"}, Enabled: true},
	}
	out, cycle := svc.ComposeWithCycleCheck(svc.Get("a"))
	if !cycle {
		t.Fatal("expected cycle to be detected")
	}
	// Should still return partial list without infinite loop.
	if len(out) == 0 {
		t.Fatal("expected at least one skill")
	}
}

func TestBuildSkillTools(t *testing.T) {
	dir := t.TempDir()
	mk := func(id, name, desc string) {
		skDir := dir + "/" + id
		if err := osMkdirAll(skDir); err != nil {
			t.Fatal(err)
		}
		content := "---\nid: " + id + "\nname: " + name + "\ndescription: " + desc + "\n---\n\nBody.\n"
		if err := osWriteFile(skDir+"/SKILL.md", []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	mk("s1", "S1", "desc1")
	mk("s2", "S2", "desc2")
	svc := NewService(dir)
	tools := svc.BuildSkillTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
}

func TestParseExtendedFields(t *testing.T) {
	dir := t.TempDir()
	skDir := dir + "/ext"
	if err := osMkdirAll(skDir); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: ext\nname: ext\ndescription: ext\nargument-hint: file path\nlicense: MIT\ncompatibility: git\neffort: high\nmodel: grok-3\nuser-invocable: true\nallowed-tools: bash,read_file\nmetadata: author=test-org,version=2.0\n---\n\nBody.\n"
	if err := osWriteFile(skDir+"/SKILL.md", []byte(content)); err != nil {
		t.Fatal(err)
	}
	svc := NewService(dir)
	sk := svc.Get("ext")
	if sk == nil {
		t.Fatal("skill not found")
	}
	if sk.ArgumentHint != "file path" {
		t.Fatalf("argument-hint=%q", sk.ArgumentHint)
	}
	if sk.License != "MIT" {
		t.Fatalf("license=%q", sk.License)
	}
	if sk.Effort != "high" {
		t.Fatalf("effort=%q", sk.Effort)
	}
	if sk.Metadata["author"] != "test-org" {
		t.Fatalf("metadata=%v", sk.Metadata)
	}
}

func TestExecuteSkill(t *testing.T) {
	dir := t.TempDir()
	skDir := dir + "/run"
	if err := osMkdirAll(skDir); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: run\nname: run\ndescription: run\n---\n\nRun $ARGUMENTS now.\n"
	if err := osWriteFile(skDir+"/SKILL.md", []byte(content)); err != nil {
		t.Fatal(err)
	}
	svc := NewService(dir)
	got, err := svc.Execute(context.Background(), "run", "deploy prod")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == "" {
		t.Fatal("empty output")
	}
}

func TestExecuteSkillNotFound(t *testing.T) {
	svc := NewService(t.TempDir())
	_, err := svc.Execute(context.Background(), "nope", "")
	if err != ErrSkillNotFound {
		t.Fatalf("expected ErrSkillNotFound, got %v", err)
	}
}
