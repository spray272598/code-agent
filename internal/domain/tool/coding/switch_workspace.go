package coding

import (
	"context"
	"fmt"
	"os"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// SwitchWorkspaceTool lets the LLM dynamically switch the working directory.
type SwitchWorkspaceTool struct {
	ws   *Workspace
	perm WorkspaceSwitcher
}

// WorkspaceSwitcher is implemented by security.Guard.
type WorkspaceSwitcher interface {
	SetSessionWorkspace(sessionID, path string)
}

func NewSwitchWorkspace(ws *Workspace, perm WorkspaceSwitcher) *SwitchWorkspaceTool {
	return &SwitchWorkspaceTool{ws: ws, perm: perm}
}

func (t *SwitchWorkspaceTool) Name() string { return "switch_workspace" }
func (t *SwitchWorkspaceTool) Description() string {
	return "Switch the agent's working directory to a different project path. " +
		"Use this when the user asks to work on a project outside the current workspace. " +
		"Args: path (absolute directory path)"
}

func (t *SwitchWorkspaceTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "absolute path to the new workspace directory (must exist)",
			},
		},
		"required": []string{"path"},
	}
}

func (t *SwitchWorkspaceTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return tool.Result{Text: "path required", IsError: true}, nil
	}

	// verify directory exists
	info, err := os.Stat(path)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("path not accessible: %v", err), IsError: true}, nil
	}
	if !info.IsDir() {
		return tool.Result{Text: fmt.Sprintf("not a directory: %s", path), IsError: true}, nil
	}

	sessionID := tool.SessionIDFrom(ctx)

	// update both Guard and Workspace
	if t.perm != nil {
		t.perm.SetSessionWorkspace(sessionID, path)
	}
	t.ws.SetSessionRoot(sessionID, path)

	oldRoot := t.ws.Root
	return tool.Result{Text: fmt.Sprintf(
		"workspace switched\n  from: %s\n  to:   %s\n\nAll subsequent file operations (read_file, write_file, edit_file, glob, grep, bash) will use the new workspace.",
		oldRoot, path,
	)}, nil
}
