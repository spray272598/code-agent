package einoorch

import (
	"context"
	"strings"
	"testing"

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

func TestGuardedToolAllow(t *testing.T) {
	g := security.NewGuard("./workspace", true, false)
	gt := &GuardedTool{Inner: echoT{}, Guard: g}
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
	gt := &GuardedTool{Inner: bashT{}, Guard: g}
	ctx := WithSession(context.Background(), "s1", false)
	out, err := gt.InvokableRun(ctx, `{"command":"rm -rf /"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "DENIED") {
		t.Fatalf("want DENIED, got %q", out)
	}
}

func TestWrapRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(echoT{})
	list := WrapRegistry(reg, nil)
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
}
