package application

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
)

// --- in-memory repos (session/message) ---

type memSessRepo struct {
	mu   sync.Mutex
	byID map[string]*sessmodel.Session
}

func newMemSessRepo() *memSessRepo { return &memSessRepo{byID: map[string]*sessmodel.Session{}} }

func (r *memSessRepo) Save(_ context.Context, s *sessmodel.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.byID[s.ID] = &cp
	return nil
}
func (r *memSessRepo) FindByID(_ context.Context, id string) (*sessmodel.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}
func (r *memSessRepo) ListByUser(_ context.Context, _ string, _ int) ([]*sessmodel.Session, error) {
	return nil, nil
}

type memMsgRepo struct {
	mu   sync.Mutex
	msgs []*sessmodel.Message
}

func newMemMsgRepo() *memMsgRepo { return &memMsgRepo{} }

func (r *memMsgRepo) Save(_ context.Context, m *sessmodel.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *m
	r.msgs = append(r.msgs, &cp)
	return nil
}
func (r *memMsgRepo) ListBySession(_ context.Context, _ string, _ int) ([]*sessmodel.Message, error) {
	return nil, nil
}
func (r *memMsgRepo) ListAsMaps(_ context.Context, _ string, _ int) ([]map[string]any, error) {
	return nil, nil
}

// --- scripted LLM: first turn emits a tool call, second turn the final answer ---

type scriptedLLM struct {
	mu    sync.Mutex
	queue []string
}

func (s *scriptedLLM) Generate(_ context.Context, _ *port.ChatRequest) (*port.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := "Final Answer: no more turns"
	if len(s.queue) > 0 {
		c = s.queue[0]
		s.queue = s.queue[1:]
	}
	return &port.ChatResponse{Content: c, TotalTokens: 15}, nil
}
func (s *scriptedLLM) GenerateStream(ctx context.Context, req *port.ChatRequest, _ func(port.StreamDelta)) (*port.ChatResponse, error) {
	return s.Generate(ctx, req)
}

// TestChatEndToEnd runs the full path: Chat → resolveSession → ReAct loop
// (Thought + Action tool call + Observation) → Final Answer → checkpoint flow.
func TestChatEndToEnd(t *testing.T) {
	// workspace with a readable file
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "README.md"), []byte("hello e2e"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := coding.NewWorkspace(wsDir)
	reg := tool.NewRegistry()
	reg.Register(coding.NewReadFile(ws))

	sessions := newMemSessRepo()
	messages := newMemMsgRepo()
	perm := security.NewGuard(wsDir, true, true)

	llm := &scriptedLLM{queue: []string{
		`Thought: 读取 README 确认内容
Action: {"name":"read_file","args":{"path":"README.md"}}`,
		`Thought: 已读取，内容确认
Final Answer: 文件内容已读取，任务完成`,
	}}

	loop := engine.NewLoop(llm, reg, sessions, messages, perm, 10, 4000)

	store := checkpoint.NewMemoryStore()
	runs := checkpoint.NewRunRegistry()
	app := New(CoreDeps{
		Loop: loop, Sessions: sessions, Messages: messages, Tools: reg, Perm: perm,
		TimeoutSec: 30, Workspace: wsDir,
	}, WithCheckpoint(store, runs))

	resp, err := app.Chat(ChatRequest{UserID: "u1", ProjectID: "p1", Message: "请读取 README 并确认内容"})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp == nil || resp.SessionID == "" {
		t.Fatalf("empty response: %#v", resp)
	}
	if resp.Response == "" {
		t.Fatal("expected a final answer")
	}

	// session persisted
	sess, _ := sessions.FindByID(context.Background(), resp.SessionID)
	if sess == nil {
		t.Fatal("session should be persisted")
	}

	// run completed (checkpoint marked completed, not running)
	snap, _ := store.Get(context.Background(), resp.SessionID)
	if snap == nil || snap.Status != checkpoint.StatusCompleted {
		t.Fatalf("expected completed checkpoint, got %#v", snap)
	}

	// not resumable after a clean finish
	if list := app.ListResumable(context.Background()); len(list) != 0 {
		t.Fatalf("cleanly-finished run should not be resumable, got %#v", list)
	}
}
