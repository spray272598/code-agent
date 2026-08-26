package host

import (
	"context"
	"fmt"
	"time"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

type ProxyTool struct {
	ToolName   string
	Desc       string
	Schema     map[string]any
	Bridge     *Bridge
	DeviceID   string
	Local      tool.ITool
	PreferHost bool
	Timeout    time.Duration
	// Strategy controls how host failures are handled.
	// Defaults to DegradeToLocal when Bridge has a heartbeat manager.
	Strategy GracefulDegradationStrategy
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
		timeout := t.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}

		strategy := t.Strategy
		if strategy == 0 && t.Bridge.HeartbeatManager() != nil {
			strategy = DegradeToLocal
		}

		localFallback := func(ctx context.Context, args map[string]any) (string, error) {
			if t.Local != nil {
				res, err := t.Local.Execute(ctx, args)
				if err != nil {
					return "", err
				}
				if res.IsError {
					return "", fmt.Errorf("%s", res.Text)
				}
				return res.Text, nil
			}
			return "", fmt.Errorf("no fallback available")
		}

		text, err := t.Bridge.CallWithDegradation(
			ctx, t.DeviceID, callID, t.ToolName, args, timeout, strategy, localFallback,
		)
		if err == nil {
			return tool.Result{Text: text}, nil
		}

		if t.Local == nil {
			return tool.Result{Text: err.Error(), IsError: true}, nil
		}

		prefix := fmt.Sprintf("[host offline/degraded: %v; fallback to local]\n", err)
		localRes, lerr := t.Local.Execute(ctx, args)
		if lerr != nil {
			return tool.Result{Text: prefix + lerr.Error(), IsError: true}, nil
		}
		if localRes.IsError {
			return tool.Result{Text: prefix + localRes.Text, IsError: true}, nil
		}
		return tool.Result{Text: prefix + localRes.Text}, nil
	}

	if t.Local != nil {
		return t.Local.Execute(ctx, args)
	}
	return tool.Result{Text: "no host agent and no local tool", IsError: true}, nil
}

// HealthInfo returns the current health of the host device.
func (t *ProxyTool) HealthInfo() (HealthInfo, bool) {
	if t.Bridge == nil || t.Bridge.HeartbeatManager() == nil {
		return HealthInfo{Status: HealthUnknown}, false
	}
	return t.Bridge.HeartbeatManager().GetDeviceHealth(t.DeviceID)
}

// IsHealthy checks if the host is currently healthy.
func (t *ProxyTool) IsHealthy() bool {
	if t.Bridge == nil || t.Bridge.HeartbeatManager() == nil {
		return false
	}
	return t.Bridge.HeartbeatManager().IsHealthy(t.DeviceID)
}
