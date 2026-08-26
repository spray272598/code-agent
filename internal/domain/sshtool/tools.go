package sshtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

// --- SSH Interactive Terminal Tool ---
//
// 暴露交互式 PTY 终端给 agent：支持打开会话、发送原始输入、读取输出、
// 调整窗口大小、关闭会话，以及一次性 run（开会话→执行命令→读取→关会话）。
// agent 负责在多轮对话间保存返回的 session_id 以实现持续交互。

type TerminalTool struct {
	term sshport.ITerminal
}

func NewTerminalTool(term sshport.ITerminal) *TerminalTool {
	return &TerminalTool{term: term}
}

func (t *TerminalTool) Name() string { return "ssh_terminal" }
func (t *TerminalTool) Description() string {
	return "Drive an interactive PTY shell on a remote SSH server. " +
		"Args: action (required: open|send|read|run|resize|close), " +
		"connection (required for open/run), session_id (required for send/read/resize/close), " +
		"data (for send), command (for run), wait_ms (for run, default 500), " +
		"cols, rows (terminal size, default 80x24). " +
		"open returns a session_id to reuse in follow-up calls."
}

func (t *TerminalTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"action":     map[string]any{"type": "string", "description": "open|send|read|run|resize|close"},
		"connection": map[string]any{"type": "string", "description": "SSH connection name (open/run)"},
		"session_id": map[string]any{"type": "string", "description": "Terminal session id from open (send/read/resize/close)"},
		"data":       map[string]any{"type": "string", "description": "Raw input to send (send)"},
		"command":    map[string]any{"type": "string", "description": "Command to run (run)"},
		"wait_ms":    map[string]any{"type": "integer", "description": "Wait after command before reading (run)"},
		"cols":       map[string]any{"type": "integer", "description": "Terminal columns"},
		"rows":       map[string]any{"type": "integer", "description": "Terminal rows"},
	}, "required": []string{"action"}}
}

func parseIntArg(args map[string]any, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			if n2, err := strconv.Atoi(n); err == nil {
				return n2
			}
		}
	}
	return def
}

func (t *TerminalTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	action, _ := args["action"].(string)
	switch action {
	case "open":
		connName, _ := args["connection"].(string)
		if connName == "" {
			return tool.Result{Text: "connection is required for open", IsError: true}, nil
		}
		cols, rows := parseIntArg(args, "cols", 80), parseIntArg(args, "rows", 24)
		sess, err := t.term.OpenTerminal(connName, cols, rows)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("open terminal error: %v", err), IsError: true}, nil
		}
		return tool.Result{Text: fmt.Sprintf("session_id: %s", sess.ID)}, nil

	case "send":
		sid, _ := args["session_id"].(string)
		data, _ := args["data"].(string)
		if sid == "" {
			return tool.Result{Text: "session_id is required for send", IsError: true}, nil
		}
		if err := t.term.Write(sid, []byte(data)); err != nil {
			return tool.Result{Text: fmt.Sprintf("send error: %v", err), IsError: true}, nil
		}
		return tool.Result{Text: "sent"}, nil

	case "read":
		sid, _ := args["session_id"].(string)
		if sid == "" {
			return tool.Result{Text: "session_id is required for read", IsError: true}, nil
		}
		out, err := t.term.Read(sid, true)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("read error: %v", err), IsError: true}, nil
		}
		return tool.Result{Text: out}, nil

	case "run":
		connName, _ := args["connection"].(string)
		command, _ := args["command"].(string)
		if connName == "" || command == "" {
			return tool.Result{Text: "connection and command are required for run", IsError: true}, nil
		}
		cols, rows := parseIntArg(args, "cols", 80), parseIntArg(args, "rows", 24)
		waitMs := parseIntArg(args, "wait_ms", 500)
		sess, err := t.term.OpenTerminal(connName, cols, rows)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("open terminal error: %v", err), IsError: true}, nil
		}
		defer t.term.Close(sess.ID)
		if err := t.term.Write(sess.ID, []byte(command+"\n")); err != nil {
			return tool.Result{Text: fmt.Sprintf("send error: %v", err), IsError: true}, nil
		}
		time.Sleep(time.Duration(waitMs) * time.Millisecond)
		out, err := t.term.Read(sess.ID, true)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("read error: %v", err), IsError: true}, nil
		}
		return tool.Result{Text: out}, nil

	case "resize":
		sid, _ := args["session_id"].(string)
		if sid == "" {
			return tool.Result{Text: "session_id is required for resize", IsError: true}, nil
		}
		cols, rows := parseIntArg(args, "cols", 80), parseIntArg(args, "rows", 24)
		if err := t.term.Resize(sid, cols, rows); err != nil {
			return tool.Result{Text: fmt.Sprintf("resize error: %v", err), IsError: true}, nil
		}
		return tool.Result{Text: "resized"}, nil

	case "close":
		sid, _ := args["session_id"].(string)
		if sid == "" {
			return tool.Result{Text: "session_id is required for close", IsError: true}, nil
		}
		if err := t.term.Close(sid); err != nil {
			return tool.Result{Text: fmt.Sprintf("close error: %v", err), IsError: true}, nil
		}
		return tool.Result{Text: "closed"}, nil

	default:
		return tool.Result{Text: fmt.Sprintf("unknown action: %q (want open|send|read|run|resize|close)", action), IsError: true}, nil
	}
}

// RegisterAll 注册所有SSH工具到工具注册表
func RegisterAll(registry tool.Registry, exec sshport.IExecutor, ft sshport.IFileTransfer, term sshport.ITerminal) {
	registry.Register(NewExecTool(exec))
	registry.Register(NewReadFileTool(ft))
	registry.Register(NewWriteFileTool(ft))
	registry.Register(NewListDirTool(ft))
	registry.Register(NewTerminalTool(term))
}
