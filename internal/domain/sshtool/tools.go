package sshtool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// --- SSH Exec Tool ---

type ExecTool struct {
	executor sshport.IExecutor
}

func NewExecTool(exec sshport.IExecutor) *ExecTool { return &ExecTool{executor: exec} }

func (t *ExecTool) Name() string { return "ssh_exec" }
func (t *ExecTool) Description() string {
	return "Execute a shell command on a remote SSH server. Args: connection (required, name of SSH connection), command (required), timeout_ms (optional, default 60000)"
}
func (t *ExecTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"connection": map[string]any{"type": "string", "description": "SSH connection name"},
		"command":    map[string]any{"type": "string", "description": "Shell command to execute"},
		"timeout_ms": map[string]any{"type": "integer", "description": "Timeout in milliseconds"},
	}, "required": []string{"connection", "command"}}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	connName, _ := args["connection"].(string)
	command, _ := args["command"].(string)
	timeoutMs := 60000
	if v, ok := args["timeout_ms"]; ok {
		switch n := v.(type) {
		case float64:
			timeoutMs = int(n)
		case int:
			timeoutMs = n
		}
	}
	if connName == "" || command == "" {
		return tool.Result{Text: "connection and command are required", IsError: true}, nil
	}
	res, err := t.executor.Exec(ctx, connName, command, time.Duration(timeoutMs)*time.Millisecond)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("SSH exec error: %v", err), IsError: true}, nil
	}
	out := fmt.Sprintf("exit_code: %d\nduration: %v\n--- output ---\n%s", res.ExitCode, res.Duration, res.Output)
	return tool.Result{Text: out, IsError: res.ExitCode != 0}, nil
}

// --- SSH Read File Tool ---

type ReadFileTool struct {
	ft sshport.IFileTransfer
}

func NewReadFileTool(ft sshport.IFileTransfer) *ReadFileTool { return &ReadFileTool{ft: ft} }

func (t *ReadFileTool) Name() string { return "ssh_read_file" }
func (t *ReadFileTool) Description() string {
	return "Read a file from a remote SSH server via SFTP. Args: connection (required), path (required)"
}
func (t *ReadFileTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"connection": map[string]any{"type": "string"},
		"path":       map[string]any{"type": "string"},
	}, "required": []string{"connection", "path"}}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	connName, _ := args["connection"].(string)
	path, _ := args["path"].(string)
	if connName == "" || path == "" {
		return tool.Result{Text: "connection and path are required", IsError: true}, nil
	}
	data, err := t.ft.ReadFile(ctx, connName, path)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("SFTP read error: %v", err), IsError: true}, nil
	}
	return tool.Result{Text: string(data)}, nil
}

// --- SSH Write File Tool ---

type WriteFileTool struct {
	ft sshport.IFileTransfer
}

func NewWriteFileTool(ft sshport.IFileTransfer) *WriteFileTool { return &WriteFileTool{ft: ft} }

func (t *WriteFileTool) Name() string { return "ssh_write_file" }
func (t *WriteFileTool) Description() string {
	return "Write content to a file on a remote SSH server via SFTP. Args: connection (required), path (required), content (required)"
}
func (t *WriteFileTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"connection": map[string]any{"type": "string"},
		"path":       map[string]any{"type": "string"},
		"content":    map[string]any{"type": "string"},
	}, "required": []string{"connection", "path", "content"}}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	connName, _ := args["connection"].(string)
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if connName == "" || path == "" {
		return tool.Result{Text: "connection and path are required", IsError: true}, nil
	}
	if err := t.ft.WriteFile(ctx, connName, path, []byte(content)); err != nil {
		return tool.Result{Text: fmt.Sprintf("SFTP write error: %v", err), IsError: true}, nil
	}
	return tool.Result{Text: fmt.Sprintf("File written: %s (%d bytes)", path, len(content))}, nil
}

// --- SSH List Dir Tool ---

type ListDirTool struct {
	ft sshport.IFileTransfer
}

func NewListDirTool(ft sshport.IFileTransfer) *ListDirTool { return &ListDirTool{ft: ft} }

func (t *ListDirTool) Name() string { return "ssh_list_dir" }
func (t *ListDirTool) Description() string {
	return "List directory contents on a remote SSH server via SFTP. Args: connection (required), path (required)"
}
func (t *ListDirTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"connection": map[string]any{"type": "string"},
		"path":       map[string]any{"type": "string"},
	}, "required": []string{"connection", "path"}}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	connName, _ := args["connection"].(string)
	path, _ := args["path"].(string)
	if connName == "" || path == "" {
		return tool.Result{Text: "connection and path are required", IsError: true}, nil
	}
	entries, err := t.ft.ListDir(ctx, connName, path)
	if err != nil {
		return tool.Result{Text: fmt.Sprintf("SFTP list error: %v", err), IsError: true}, nil
	}
	b, _ := json.MarshalIndent(entries, "", "  ")
	return tool.Result{Text: string(b)}, nil
}

// RegisterAll 注册所有SSH工具到工具注册表
func RegisterAll(registry tool.Registry, exec sshport.IExecutor, ft sshport.IFileTransfer) {
	registry.Register(NewExecTool(exec))
	registry.Register(NewReadFileTool(ft))
	registry.Register(NewWriteFileTool(ft))
	registry.Register(NewListDirTool(ft))
}
