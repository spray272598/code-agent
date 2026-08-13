package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

type fakeLLM struct {
	out string
	err error
}

func (f *fakeLLM) Generate(_ context.Context, _ *port.ChatRequest) (*port.ChatResponse, error) {
	return &port.ChatResponse{Content: f.out}, f.err
}

func (f *fakeLLM) GenerateStream(_ context.Context, _ *port.ChatRequest, _ func(port.StreamDelta)) (*port.ChatResponse, error) {
	return f.Generate(context.Background(), nil)
}

func TestParseSkillMatch(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`{"skill":"ssh-ops"}`, "ssh-ops"},
		{`Sure: {"skill":"docs"}`, "docs"},
		{`{"skill":""}`, ""},
		{`no json`, ""},
	}
	for _, tt := range tests {
		if got := parseSkillMatch(tt.in); got != tt.want {
			t.Errorf("parseSkillMatch(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchSemanticFallsBackToLLM(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "ssh-ops", "SSH 运维", "remote server ops", "运维")
	svc := NewService(dir)
	svc.SetLLM(&fakeLLM{out: `{"skill":"ssh-ops"}`})

	// rule Match should miss natural-language phrasing
	if svc.Match("帮我看下线上服务器负载") != nil {
		t.Fatal("rule Match should miss without literal trigger")
	}
	got := svc.MatchSemantic(context.Background(), "帮我看下线上服务器负载")
	if got == nil || got.ID != "ssh-ops" {
		t.Fatalf("MatchSemantic = %#v, want ssh-ops", got)
	}
}

func TestMatchSemanticNoLLM(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "s1", "S1", "desc", "trigger")
	svc := NewService(dir) // no SetLLM
	if got := svc.MatchSemantic(context.Background(), "anything"); got != nil {
		t.Fatalf("expected nil without LLM, got %#v", got)
	}
}

func writeSkill(t *testing.T, dir, id, name, desc, triggers string) {
	t.Helper()
	sub := filepath.Join(dir, id)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: " + id + "\nname: " + name + "\ndescription: " + desc + "\ntriggers: " + triggers + "\n---\n\nBody here.\n"
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
