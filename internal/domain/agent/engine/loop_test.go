package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
)

// --- mocks ---

type scriptedLLM struct {
	mu    sync.Mutex
	queue []string
	calls int
}

func (s *scriptedLLM) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.queue) == 0 {
		return &port.ChatResponse{Content: "Final Answer: fallback"}, nil
	}
	c := s.queue[0]
	s.queue = s.queue[1:]
	return &port.ChatResponse{Content: c, TotalTokens: 10}, nil
}

func (s *scriptedLLM) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(delta port.StreamDelta)) (*port.ChatResponse, error) {
	return s.Generate(ctx, req)
}

type memSessionRepo struct {
	mu   sync.Mutex
	byID map[string]*sessmodel.Session
}

func newMemSessionRepo() *memSessionRepo {
	return &memSessionRepo{byID: map[string]*sessmodel.Session{}}
}
func (r *memSessionRepo) Save(ctx context.Context, s *sessmodel.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.byID[s.ID] = &cp
	return nil
}
func (r *memSessionRepo) FindByID(ctx context.Context, id string) (*sessmodel.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id], nil
}
func (r *memSessionRepo) ListByUser(ctx context.Context, userID string, limit int) ([]*sessmodel.Session, error) {
	return nil, nil
}

type memMsgRepo struct {
	mu   sync.Mutex
	msgs []*sessmodel.Message
}

func (r *memMsgRepo) Save(ctx context.Context, m *sessmodel.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *m
	r.msgs = append(r.msgs, &cp)
	return nil
}
func (r *memMsgRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]*sessmodel.Message, error) {
	return nil, nil
}
func (r *memMsgRepo) ListAsMaps(ctx context.Context, sessionID string, limit int) ([]map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.msgs))
	for _, m := range r.msgs {
		if m.SessionID != sessionID {
			continue
		}
		out = append(out, map[string]any{
			"role": m.Role, "content": m.Content, "toolName": m.ToolName, "toolCallId": m.ToolCallID,
		})
	}
	return out, nil
}

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "echo arg text" }
func (echoTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (echoTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	t, _ := args["text"].(string)
	return tool.Result{Text: "echo:" + t}, nil
}

func setupLoop(t *testing.T, llm port.ILLMPort, confirmWrite bool) (*Loop, *security.Guard) {
	t.Helper()
	dir := t.TempDir()
	ws := coding.NewWorkspace(dir)
	reg := tool.NewRegistry()
	reg.Register(coding.NewReadFile(ws))
	reg.Register(coding.NewWriteFile(ws))
	reg.Register(coding.NewEditFile(ws))
	reg.Register(coding.NewGlob(ws))
	reg.Register(echoTool{})
	perm := security.NewGuard(dir, true, confirmWrite)
	loop := NewLoop(llm, reg, newMemSessionRepo(), &memMsgRepo{}, perm, 8, 8000)
	return loop, perm
}

func TestLoopFinalAnswerOnly(t *testing.T) {
	llm := &scriptedLLM{queue: []string{
		"Thought: simple Q\nFinal Answer: hello world",
	}}
	loop, _ := setupLoop(t, llm, false)
	sess := sessmodel.NewSession("s1", "u1", "p1", "t", "")
	res, err := loop.Run(context.Background(), sess, "hi", nil, RunOptions{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Response, "hello world") {
		t.Fatalf("response=%q", res.Response)
	}
	if res.ToolCalls != 0 {
		t.Fatalf("toolCalls=%d", res.ToolCalls)
	}
}

func TestLoopThoughtActionObservation(t *testing.T) {
	llm := &scriptedLLM{queue: []string{
		`Thought: call echo
Action: {"name":"echo","args":{"text":"ping"}}`,
		`Thought: observed result
Final Answer: got ping`,
	}}
	loop, _ := setupLoop(t, llm, false)
	sess := sessmodel.NewSession("s2", "u1", "p1", "t", "")
	var events []*Event
	ch := make(chan *Event, 64)
	done := make(chan struct{})
	go func() {
		for e := range ch {
			events = append(events, e)
		}
		close(done)
	}()
	res, err := loop.Run(context.Background(), sess, "run echo", ch, RunOptions{AutoApprove: true})
	close(ch)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if res.ToolCalls != 1 {
		t.Fatalf("toolCalls=%d res=%+v", res.ToolCalls, res)
	}
	if !strings.Contains(res.Response, "ping") {
		t.Fatalf("response=%q", res.Response)
	}
	var hasThought, hasAction, hasObs bool
	for _, e := range events {
		switch e.Type {
		case EventThought:
			if strings.Contains(e.Content, "call echo") || strings.Contains(e.Content, "observed") {
				hasThought = true
			}
		case EventAction, EventToolCall:
			hasAction = true
		case EventObservation, EventToolResult:
			hasObs = true
		}
	}
	if !hasThought || !hasAction || !hasObs {
		t.Fatalf("ReAct events incomplete thought=%v action=%v obs=%v events=%d", hasThought, hasAction, hasObs, len(events))
	}
}

func TestLoopPermissionConfirmMCP(t *testing.T) {
	// MCP-style tool name must go through guard → confirm
	reg := tool.NewRegistry()
	reg.Register(&namedTool{name: "demo__dangerous", fn: func() tool.Result {
		return tool.Result{Text: "should not run"}
	}})
	llm := &scriptedLLM{queue: []string{
		`Thought: use mcp
Action: {"name":"demo__dangerous","args":{"x":1}}`,
	}}
	perm := security.NewGuard("./workspace", true, true)
	loop := NewLoop(llm, reg, newMemSessionRepo(), &memMsgRepo{}, perm, 5, 4000)
	sess := sessmodel.NewSession("s3", "u1", "p1", "t", "")
	res, err := loop.Run(context.Background(), sess, "mcp", nil, RunOptions{AutoApprove: false})
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedPermission {
		t.Fatalf("expected permission confirm, got %+v", res)
	}
}

func TestLoopPermissionDenyBash(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(coding.NewBash(coding.NewWorkspace(t.TempDir()), 5))
	llm := &scriptedLLM{queue: []string{
		`Thought: bad
Action: {"name":"bash","args":{"command":"rm -rf /"}}`,
		`Thought: denied
Final Answer: blocked`,
	}}
	perm := security.NewGuard(t.TempDir(), true, true)
	loop := NewLoop(llm, reg, newMemSessionRepo(), &memMsgRepo{}, perm, 5, 4000)
	sess := sessmodel.NewSession("s4", "u1", "p1", "t", "")
	res, err := loop.Run(context.Background(), sess, "danger", nil, RunOptions{AutoApprove: false})
	if err != nil {
		t.Fatal(err)
	}
	// deny should not halt with NeedPermission; continue to final
	if res.NeedPermission {
		t.Fatalf("deny should not set NeedPermission: %+v", res)
	}
}

type namedTool struct {
	name string
	fn   func() tool.Result
}

func (n *namedTool) Name() string                { return n.name }
func (n *namedTool) Description() string         { return n.name }
func (n *namedTool) InputSchema() map[string]any { return map[string]any{} }
func (n *namedTool) Execute(context.Context, map[string]any) (tool.Result, error) {
	return n.fn(), nil
}

// --- 3.5: plan visualization + interruptible re-planning ---

type failNTimesTool struct {
	mu    sync.Mutex
	fails int
}

func (f *failNTimesTool) Name() string        { return "fail" }
func (f *failNTimesTool) Description() string { return "fails the first N times" }
func (f *failNTimesTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (f *failNTimesTool) Execute(_ context.Context, _ map[string]any) (tool.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return tool.Result{Text: "boom", IsError: true}, nil
	}
	return tool.Result{Text: "ok"}, nil
}

func TestLoopPlanAutoReplanOnStall(t *testing.T) {
	ft := &failNTimesTool{fails: 3}
	reg := tool.NewRegistry()
	reg.Register(ft)
	perm := security.NewGuard("./workspace", true, true)
	llm := &scriptedLLM{queue: []string{
		`Thought: explore
Action: {"name":"fail","args":{}}`,
		`Thought: retry
Action: {"name":"fail","args":{}}`,
		`Thought: retry2
Action: {"name":"fail","args":{}}`,
		`Thought: recovered
Final Answer: survived replan`,
	}}
	loop := NewLoop(llm, reg, newMemSessionRepo(), &memMsgRepo{}, perm, 8, 8000)
	// message triggers a multi-step plan
	sess := sessmodel.NewSession("s5", "u1", "p1", "t", "")
	ch := make(chan *Event, 128)
	go func() {
		for range ch {
		}
	}()
	res, err := loop.Run(context.Background(), sess, "先探索然后修改文件并且验证", ch, RunOptions{AutoApprove: true})
	close(ch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Response, "survived replan") {
		t.Fatalf("response=%q", res.Response)
	}
}

func TestLoopPlanEmitVisualization(t *testing.T) {
	llm := &scriptedLLM{queue: []string{
		`Thought: explore
Action: {"name":"echo","args":{"text":"x"}}`,
		`Thought: done
Final Answer: ok`,
	}}
	loop, _ := setupLoop(t, llm, false)
	sess := sessmodel.NewSession("s6", "u1", "p1", "t", "")
	var events []*Event
	ch := make(chan *Event, 128)
	done := make(chan struct{})
	go func() {
		for e := range ch {
			events = append(events, e)
		}
		close(done)
	}()
	_, err := loop.Run(context.Background(), sess, "先探索然后修改文件并且验证", ch, RunOptions{AutoApprove: true})
	close(ch)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	var planUpdates, planEvents int
	for _, e := range events {
		switch e.Type {
		case EventPlanUpdate:
			planUpdates++
			if e.Data == nil {
				t.Fatalf("plan_update missing data")
			}
		case EventPlan:
			planEvents++
		}
	}
	if planEvents == 0 {
		t.Fatal("expected at least one EventPlan")
	}
	if planUpdates == 0 {
		t.Fatalf("expected plan update events for visualization, got %d", planUpdates)
	}
}

// gatedLLM blocks on its first Generate until gate is closed, so a control
// signal can be deterministically queued before any step executes.
type gatedLLM struct {
	mu    sync.Mutex
	queue []string
	calls int
	gate  chan struct{}
}

func (s *gatedLLM) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		<-s.gate // wait until the test has sent its control signal
	}
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.mu.Unlock()
		return &port.ChatResponse{Content: "Final Answer: fallback"}, nil
	}
	c := s.queue[0]
	s.queue = s.queue[1:]
	s.mu.Unlock()
	return &port.ChatResponse{Content: c, TotalTokens: 10}, nil
}

func (s *gatedLLM) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(delta port.StreamDelta)) (*port.ChatResponse, error) {
	return s.Generate(ctx, req)
}

func TestLoopPlanUserReplan(t *testing.T) {
	llm := &gatedLLM{gate: make(chan struct{})}
	llm.queue = []string{
		`Thought: step1
Action: {"name":"echo","args":{"text":"x"}}`,
		`Thought: after replan
Final Answer: replanned`,
	}
	loop, _ := setupLoop(t, llm, false)
	sess := sessmodel.NewSession("s7", "u1", "p1", "t", "")
	ctrlCh := make(chan Control, 4)
	ch := make(chan *Event, 128)
	var gotReplan bool
	done := make(chan struct{})
	go func() {
		for e := range ch {
			if e.Type == EventReplan {
				gotReplan = true
			}
		}
		close(done)
	}()
	runDone := make(chan struct{})
	go func() {
		loop.Run(context.Background(), sess, "先探索然后修改文件并且验证", ch, RunOptions{AutoApprove: true, ControlCh: ctrlCh})
		close(runDone)
	}()
	// queue the control signal, then unblock the first LLM call so the loop
	// proceeds with the signal already buffered for the step-2 drain.
	ctrlCh <- Control{Signal: ControlReplanWithGoal, Goal: "新目标：重构整个模块并且运行测试"}
	close(llm.gate)
	<-runDone
	close(ch)
	<-done
	if !gotReplan {
		t.Fatal("expected EventReplan after user control signal")
	}
}

func TestLoopPlanModeExploreCheckpoint(t *testing.T) {
	llm := &scriptedLLM{queue: []string{
		`Thought: explore
Action: {"name":"read_file","args":{"path":"a.txt"}}`,
		`Thought: done
Final Answer: ok`,
	}}
	loop, _ := setupLoop(t, llm, false)
	sess := sessmodel.NewSession("s8", "u1", "p1", "t", "")
	ctrlCh := make(chan Control, 4)
	ch := make(chan *Event, 128)
	var gotExplore, gotImplement bool
	done := make(chan struct{})
	go func() {
		for e := range ch {
			if e.Type == EventCheckpoint {
				if e.SubType == "plan_explore" {
					gotExplore = true
				}
				if e.SubType == "plan_implement" {
					gotImplement = true
				}
			}
		}
		close(done)
	}()
	runDone := make(chan struct{})
	go func() {
		loop.Run(context.Background(), sess, "先探索然后修改文件并且验证", ch, RunOptions{AutoApprove: true, ControlCh: ctrlCh})
		close(runDone)
	}()
	// enter explore phase then immediately exit to implement
	ctrlCh <- Control{Signal: ControlPlanExplore}
	ctrlCh <- Control{Signal: ControlPlanImplement}
	<-runDone
	close(ch)
	<-done
	if !gotExplore {
		t.Fatal("expected plan_explore checkpoint")
	}
	if !gotImplement {
		t.Fatal("expected plan_implement checkpoint")
	}
}
