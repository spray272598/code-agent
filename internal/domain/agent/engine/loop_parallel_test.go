package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

type slowEcho struct {
	name string
	n    *atomic.Int32
	max  *atomic.Int32
}

func (s *slowEcho) Name() string        { return s.name }
func (s *slowEcho) Description() string { return "slow" }
func (s *slowEcho) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"text": map[string]any{"type": "string"},
	}, "required": []string{"text"}}
}
func (s *slowEcho) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	cur := s.n.Add(1)
	for {
		old := s.max.Load()
		if cur <= old || s.max.CompareAndSwap(old, cur) {
			break
		}
	}
	time.Sleep(80 * time.Millisecond)
	s.n.Add(-1)
	t, _ := args["text"].(string)
	return tool.Result{Text: "ok:" + t}, nil
}

func TestLoopParallelReadOnlyTools(t *testing.T) {
	var concurrent, maxC atomic.Int32
	reg := tool.NewRegistry()
	// names contain "read" so IsReadOnly=true → parallel batch
	reg.Register(&slowEcho{name: "read_a", n: &concurrent, max: &maxC})
	reg.Register(&slowEcho{name: "read_b", n: &concurrent, max: &maxC})
	reg.Register(&slowEcho{name: "read_c", n: &concurrent, max: &maxC})
	llm := &scriptedLLM{queue: []string{
		`Thought: parallel reads
Action: [{"name":"read_a","args":{"text":"a"}},{"name":"read_b","args":{"text":"b"}},{"name":"read_c","args":{"text":"c"}}]`,
		`Thought: done
Final Answer: parallel ok`,
	}}
	loop := NewLoop(llm, reg, newMemSessionRepo(), &memMsgRepo{}, nil, 5, 8000)
	sess := sessmodel.NewSession("sp", "u", "p", "t", "")
	res, err := loop.Run(context.Background(), sess, "parallel", nil, RunOptions{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ToolCalls != 3 {
		t.Fatalf("toolCalls=%d", res.ToolCalls)
	}
	if maxC.Load() < 2 {
		t.Fatalf("expected parallel concurrency >=2, max=%d", maxC.Load())
	}
	if !strings.Contains(res.Response, "parallel") {
		t.Fatalf("response=%q", res.Response)
	}
}

func TestLoopValidationBlocks(t *testing.T) {
	// reflect() also calls LLM once after validation failure
	llm := &scriptedLLM{queue: []string{
		`Thought: bad args
Action: {"name":"read_x","args":{}}`,
		`root cause: missing text; next: provide text arg`,
		`Thought: fix
Final Answer: validated`,
	}}
	reg := tool.NewRegistry()
	var n, m atomic.Int32
	reg.Register(&slowEcho{name: "read_x", n: &n, max: &m})
	loop := NewLoop(llm, reg, newMemSessionRepo(), &memMsgRepo{}, nil, 5, 4000)
	sess := sessmodel.NewSession("sv", "u", "p", "t", "")
	res, err := loop.Run(context.Background(), sess, "val", nil, RunOptions{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Response, "validated") {
		t.Fatalf("%q", res.Response)
	}
}

func TestLoopTokenBudgetStop(t *testing.T) {
	llm2 := &scriptedLLM{queue: []string{
		`{"name":"read_x","args":{"text":"a"}}`,
		`{"name":"read_x","args":{"text":"b"}}`,
		`{"name":"read_x","args":{"text":"c"}}`,
		`Final Answer: still going`,
	}}
	reg := tool.NewRegistry()
	var n, m atomic.Int32
	reg.Register(&slowEcho{name: "read_x", n: &n, max: &m})
	loop := NewLoop(llm2, reg, newMemSessionRepo(), &memMsgRepo{}, nil, 10, 15)
	sess := sessmodel.NewSession("sb", "u", "p", "t", "")
	res, err := loop.Run(context.Background(), sess, "budget", nil, RunOptions{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.TokenUsed == 0 {
		t.Fatal("expected tokens counted")
	}
	_ = port.ChatResponse{}
}
