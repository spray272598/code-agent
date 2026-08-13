package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// fakeLLM returns a canned response for the single extraction call.
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

func TestParseExtracted(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantN   int
		wantCat string
	}{
		{"clean array", `[{"category":"preference","content":"use english comments","importance":80}]`, 1, "preference"},
		{"noisy wrap", `Here you go: [{"category":"correction","content":"注释用英文","importance":70}] hope this helps`, 1, "correction"},
		{"empty array", `[]`, 0, ""},
		{"multi", `[{"category":"fact","content":"a"},{"category":"preference","content":"b"}]`, 2, "fact"},
		{"blank content filtered", `[{"category":"x","content":"  "},{"category":"preference","content":"real"}]`, 1, "preference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := parseExtracted(tt.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(items) != tt.wantN {
				t.Fatalf("got %d items, want %d (%#v)", len(items), tt.wantN, items)
			}
			if tt.wantN > 0 && items[0].Category != tt.wantCat {
				t.Fatalf("first category=%q want %q", items[0].Category, tt.wantCat)
			}
		})
	}
}

func TestParseExtractedNoJSON(t *testing.T) {
	if _, err := parseExtracted("no json here"); err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestLLMExtractorExtract(t *testing.T) {
	e := NewLLMExtractor(&fakeLLM{out: `[{"category":"preference","content":"prefer go test","importance":80}]`})
	items, err := e.Extract(context.Background(), "以后都用 go test")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(items) != 1 || items[0].Content != "prefer go test" {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestLLMExtractorNoLLM(t *testing.T) {
	e := NewLLMExtractor(nil)
	if _, err := e.Extract(context.Background(), "x"); err == nil {
		t.Fatal("expected error when no LLM configured")
	}
}

func TestLooksMemoryIntent(t *testing.T) {
	pos := []string{"以后用英文注释", "记住我的用户名是 bob", "prefer go test", "别再写中文注释"}
	neg := []string{"帮我读一下 README", "hello", "列出所有文件", "执行 echo hello"}
	for _, s := range pos {
		if !looksMemoryIntent(s) {
			t.Errorf("expected memory intent for %q", s)
		}
	}
	for _, s := range neg {
		if looksMemoryIntent(s) {
			t.Errorf("did not expect memory intent for %q", s)
		}
	}
	// case-insensitive English trigger
	if !looksMemoryIntent(strings.ToUpper("always use tabs")) {
		t.Error("expected case-insensitive match")
	}
}
