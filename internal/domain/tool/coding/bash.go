package coding

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

type BashTool struct {
	ws      *Workspace
	timeout time.Duration
	// ProcessIsolate puts the command in a new process group (Unix) so kills
	// don't take down the agent; on Windows uses CREATE_NEW_PROCESS_GROUP.
	ProcessIsolate bool
}

func NewBash(ws *Workspace, timeoutSec int) *BashTool {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	return &BashTool{ws: ws, timeout: time.Duration(timeoutSec) * time.Second}
}

// NewBashIsolated creates a bash tool with process-level isolation.
func NewBashIsolated(ws *Workspace, timeoutSec int) *BashTool {
	b := NewBash(ws, timeoutSec)
	b.ProcessIsolate = true
	return b
}

func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Description() string {
	return "Run a shell command in the workspace. Args: command (required)"
}
func (t *BashTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"command": map[string]any{"type": "string"},
	}, "required": []string{"command"}}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	cmdStr, _ := args["command"].(string)
	if cmdStr == "" {
		return tool.Result{Text: "command required", IsError: true}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
	} else {
		cmd = exec.CommandContext(cctx, "bash", "-lc", cmdStr)
	}
	cmd.Dir = t.ws.Root
	if t.ProcessIsolate {
		setProcessIsolate(cmd)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if stderr.Len() > 0 {
		out += "\n[stderr]\n" + stderr.String()
	}
	out = common.TruncateRunes(out, 6000)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("exit error: %v\n%s", err, out), IsError: true}, nil
	}
	if out == "" {
		out = "(empty output)"
	}
	return tool.Result{Text: out}, nil
}
