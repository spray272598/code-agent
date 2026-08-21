package intent

import (
	"strings"
	"testing"
)

func TestClassifier_Coreference_SSH(t *testing.T) {
	c := NewClassifier(nil)
	ec := &EntityContext{LastSSHConnection: "web"}
	res := c.ClassifyWithContext("在那台机器上执行 ls -l", ec)
	if !strings.Contains(res.CleanInput, "web") {
		t.Fatalf("coref not resolved: %q", res.CleanInput)
	}
	if strings.Contains(res.CleanInput, "那台机器") {
		t.Fatalf("pronoun left in output: %q", res.CleanInput)
	}
	if !strings.Contains(res.Source, "coref") {
		t.Fatalf("source should mark coref, got %q", res.Source)
	}
	// 意图不应被 coref 改变（无 deep/team 前缀 → normal）
	if res.Intent != IntentNormal {
		t.Fatalf("intent changed by coref: %v", res.Intent)
	}
}

func TestClassifier_Coreference_NoEntityUnchanged(t *testing.T) {
	c := NewClassifier(nil)
	ec := &EntityContext{} // 空字段
	res := c.ClassifyWithContext("在那台机器上执行 ls", ec)
	if res.CleanInput != "在那台机器上执行 ls" {
		t.Fatalf("should be unchanged when no entity: %q", res.CleanInput)
	}
	if strings.Contains(res.Source, "coref") {
		t.Fatalf("source should not mark coref: %q", res.Source)
	}
}

func TestClassifier_Coreference_FileAndDir(t *testing.T) {
	c := NewClassifier(nil)
	ec := &EntityContext{LastFile: "main.go", LastDir: "/src"}
	if res := c.ClassifyWithContext("打开刚才那个文件看看", ec); !strings.Contains(res.CleanInput, "main.go") {
		t.Fatalf("file coref failed: %q", res.CleanInput)
	}
	if res := c.ClassifyWithContext("切换到那个目录", ec); !strings.Contains(res.CleanInput, "/src") {
		t.Fatalf("dir coref failed: %q", res.CleanInput)
	}
}

func TestClassifier_ClassifyWithContextNilEqualsClassify(t *testing.T) {
	c := NewClassifier(nil)
	a := c.Classify("帮我并行调研几个方案")
	b := c.ClassifyWithContext("帮我并行调研几个方案", nil)
	if a.Intent != b.Intent || a.CleanInput != b.CleanInput {
		t.Fatalf("nil context should equal Classify: %+v vs %+v", a, b)
	}
}

func TestExtractEntities(t *testing.T) {
	contents := []string{
		`user: 帮我连上 web 执行命令`,
		`tool: {"connection":"web","command":"ls"}`,
		`tool: {"path":"/etc/hosts","content":"x"}`,
		`tool: {"session_id":"term-123","connection":"db"}`,
	}
	ec := ExtractEntities(contents)
	if ec == nil {
		t.Fatal("expected entities")
	}
	// 取最后一次出现：connection 应为 db，path 为 /etc/hosts
	if ec.LastSSHConnection != "db" {
		t.Fatalf("LastSSHConnection=%q want db", ec.LastSSHConnection)
	}
	if ec.LastFile != "/etc/hosts" {
		t.Fatalf("LastFile=%q want /etc/hosts", ec.LastFile)
	}
	if ec.LastSessionID != "term-123" {
		t.Fatalf("LastSessionID=%q want term-123", ec.LastSessionID)
	}
}

func TestExtractEntities_Empty(t *testing.T) {
	if ec := ExtractEntities([]string{"普通对话", "你好"}); ec != nil {
		t.Fatalf("expected nil when no entities, got %+v", ec)
	}
}
