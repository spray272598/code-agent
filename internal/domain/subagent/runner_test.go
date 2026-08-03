package subagent

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
)

type stubLLM struct{}

func (s *stubLLM) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	// first call: tool, second: final (detect tool history)
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "tool" {
			return &port.ChatResponse{Content: "explored: done", TotalTokens: 10}, nil
		}
	}
	return &port.ChatResponse{Content: `{"name":"glob","args":{"pattern":"*"}}`, TotalTokens: 5}, nil
}
func (s *stubLLM) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(port.StreamDelta)) (*port.ChatResponse, error) {
	return s.Generate(ctx, req)
}

func TestRunAllParallel(t *testing.T) {
	ws := coding.NewWorkspace(t.TempDir())
	reg := tool.NewRegistry()
	reg.Register(coding.NewGlob(ws))
	reg.Register(coding.NewReadFile(ws))
	r := NewRunner(&stubLLM{}, reg, ws.Root)
	r.MaxConcurrent = 2
	var progress int
	r.OnProgress = func(p Progress) { progress++ }
	res := r.RunAll(context.Background(), []Spec{
		{ID: "a", Prompt: "list files", Role: "explore"},
		{ID: "b", Prompt: "list again", Role: "explore"},
	})
	if len(res) != 2 {
		t.Fatalf("got %d", len(res))
	}
	if res[0].Status != "ok" || res[1].Status != "ok" {
		t.Fatalf("%+v", res)
	}
	if progress == 0 {
		t.Fatal("expected progress events")
	}
}
