package host

import (
	"context"
	"fmt"
	"time"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ProxyTool executes a tool on the connected host agent; optional local fallback.
type ProxyTool struct {
	ToolName string
	Desc     string
	Schema   map[string]any
	Bridge   *Bridge
	DeviceID string
	Local    tool.ITool // fallback when host offline
	PreferHost bool
	Timeout  time.Duration
}

func (t *ProxyTool) Name() string { return t.ToolName }
func (t *ProxyTool) Description() string {
	if t.Desc != "" {
		return t.Desc + " [host-capable]"
	}
	return t.ToolName + " (host proxy)"
}
func (t *ProxyTool) InputSchema() map[string]any {
	if t.Schema != nil {
		return t.Schema
	}
	return map[string]any{"type": "object"}
}

func (t *ProxyTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	if t.PreferHost && t.Bridge != nil && t.Bridge.OnlineCount() > 0 {
		callID := fmt.Sprintf("hc-%d", time.Now().UnixNano())
		text, err := t.Bridge.Call(ctx, t.DeviceID, callID, t.ToolName, args, t.Timeout)
		if err == nil {
			return tool.Result{Text: text}, nil
		}
		// fall through to local if available
		if t.Local == nil {
			return tool.Result{Text: err.Error(), IsError: true}, nil
		}
		text = fmt.Sprintf("[host offline/error: %v; fallback local]\n", err)
		localRes, lerr := t.Local.Execute(ctx, args)
		if lerr != nil {
			return tool.Result{Text: text + lerr.Error(), IsError: true}, nil
		}
		if localRes.IsError {
			return tool.Result{Text: text + localRes.Text, IsError: true}, nil
		}
		return tool.Result{Text: text + localRes.Text}, nil
	}
	if t.Local != nil {
		return t.Local.Execute(ctx, args)
	}
	return tool.Result{Text: "no host agent and no local tool", IsError: true}, nil
}
