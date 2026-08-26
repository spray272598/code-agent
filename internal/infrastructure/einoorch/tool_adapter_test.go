package einoorch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

type echoT struct{}

func (echoT) Name() string        { return "echo" }
func (echoT) Description() string { return "echo" }
func (echoT) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"text": map[string]any{"type": "string"},
	}, "required": []string{"text"}}
}

func (echoT) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	t, _ := args["text"].(string)
	return tool.Result{Text: "echo:" + t}, nil
}

type readT struct{}

func (readT) Name() string        { return "read_file" }
func (readT) Description() string { return "Read file contents" }
func (readT) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"},
	}}
}

func (readT) Execute(_ context.Context, _ map[string]any) (tool.Result, error) {
	return tool.Result{Text: "file content"}, nil
}

type editT struct{}

func (editT) Name() string        { return "edit_file" }
func (editT) Description() string { return "Edit file contents" }
func (editT) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"},
	}}
}

func (editT) Execute(_ context.Context, _ map[string]any) (tool.Result, error) {
	return tool.Result{Text: "edited"}, nil
}

type grepT struct{}

func (grepT) Name() string        { return "code_search" }
func (grepT) Description() string { return "Search code patterns" }
func (grepT) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"pattern": map[string]any{"type": "string"},
	}}
}

func (grepT) Execute(_ context.Context, _ map[string]any) (tool.Result, error) {
	return tool.Result{Text: "matches"}, nil
}

type bashT struct{}

func (bashT) Name() string        { return "bash" }
func (bashT) Description() string { return "bash" }
func (bashT) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"command": map[string]any{"type": "string"},
	}}
}

func (bashT) Execute(_ context.Context, _ map[string]any) (tool.Result, error) {
	return tool.Result{Text: "should-not-run"}, nil
}

type memAudit struct {
	entries []audit.Entry
}

func (m *memAudit) Append(_ context.Context, e audit.Entry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) ListBySession(context.Context, string, string, int) ([]audit.Entry, error) {
	return m.entries, nil
}

func (m *memAudit) ListForUser(context.Context, string, int) ([]audit.Entry, error) {
	return m.entries, nil
}

func TestGuardedToolAllow(t *testing.T) {
	g := security.NewGuard("./workspace", true, false)
	gt := &GuardedTool{Inner: echoT{}, Guard: g, UseInterrupt: false}
	ctx := WithSession(context.Background(), "s1", true)
	out, err := gt.InvokableRun(ctx, `{"text":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo:hi" {
		t.Fatalf("got %q", out)
	}
}

func TestGuardedToolDenyBash(t *testing.T) {
	g := security.NewGuard("./workspace", true, true)
	gt := &GuardedTool{Inner: bashT{}, Guard: g, UseInterrupt: false}
	ctx := WithSession(context.Background(), "s1", false)
	out, err := gt.InvokableRun(ctx, `{"command":"rm -rf /"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "DENIED") {
		t.Fatalf("want DENIED, got %q", out)
	}
}

func TestGuardedToolValidation(t *testing.T) {
	gt := &GuardedTool{Inner: echoT{}, UseInterrupt: false}
	out, err := gt.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "validation error") {
		t.Fatalf("got %q", out)
	}
}

func TestGuardedToolHookAbort(t *testing.T) {
	bus := hook.NewBus()
	bus.On(hook.PreToolUse, func(ctx context.Context, ev hook.Event) error {
		return hook.Abort("policy")
	})
	gt := &GuardedTool{
		Inner: echoT{}, Guard: security.NewGuard("./ws", true, false),
		UseInterrupt: false,
		Cross:        &CrossCut{Hooks: bus},
	}
	ctx := WithRunContext(context.Background(), RunContext{SessionID: "s", AutoApprove: true, Cross: &CrossCut{Hooks: bus}})
	out, err := gt.InvokableRun(ctx, `{"text":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "HOOK_ABORT") {
		t.Fatalf("got %q", out)
	}
}

func TestGuardedToolCacheAndAudit(t *testing.T) {
	aud := &memAudit{}
	cache := tool.NewResultCache(time.Minute, 16)
	// mark echo as cacheable via name containing "echo" - IsCacheable checks IsReadOnly
	// echo is in IsReadOnly list
	gt := &GuardedTool{
		Inner: echoT{}, UseInterrupt: false,
		Cross: &CrossCut{Audit: aud, Cache: cache, UserID: "u1"},
	}
	ctx := WithRunContext(context.Background(), RunContext{
		SessionID: "s1", UserID: "u1", AutoApprove: true,
		Cross: &CrossCut{Audit: aud, Cache: cache, UserID: "u1"},
	})
	out1, _ := gt.InvokableRun(ctx, `{"text":"a"}`)
	out2, _ := gt.InvokableRun(ctx, `{"text":"a"}`)
	if out1 != out2 || out1 != "echo:a" {
		t.Fatalf("%q %q", out1, out2)
	}
	if len(aud.entries) < 2 {
		t.Fatalf("audit entries=%d", len(aud.entries))
	}
	// second should be cache decision
	if aud.entries[len(aud.entries)-1].Decision != "cache" {
		t.Fatalf("last decision=%s", aud.entries[len(aud.entries)-1].Decision)
	}
}

func TestGuardedToolConfirm(t *testing.T) {
	g := security.NewGuard("./workspace", true, true)
	// write-like confirm
	wt := &writeT{}
	gt := &GuardedTool{Inner: wt, Guard: g, UseInterrupt: false}
	ctx := WithSession(context.Background(), "s1", false)
	out, err := gt.InvokableRun(ctx, `{"path":"a.txt","content":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CONFIRM") {
		t.Fatalf("got %q", out)
	}
	if len(g.ListPending("s1")) != 1 {
		t.Fatal("pending expected")
	}
}

type writeT struct{}

func (writeT) Name() string        { return "write_file" }
func (writeT) Description() string { return "write" }
func (writeT) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"},
	}}
}

func (writeT) Execute(context.Context, map[string]any) (tool.Result, error) {
	return tool.Result{Text: "wrote"}, nil
}

func TestWrapRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	list := WrapRegistry(reg, nil)
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
}
