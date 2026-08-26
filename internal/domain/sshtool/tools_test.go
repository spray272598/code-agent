package sshtool

import (
	"context"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// fakeTerminal is an in-memory ITerminal for testing the tool dispatch logic.
type fakeTerminal struct {
	opened  int
	written []string
	closed  int
	resized int
	buf     string
}

func (f *fakeTerminal) OpenTerminal(connName string, cols, rows int) (*model.TerminalSession, error) {
	f.opened++
	return &model.TerminalSession{ID: "sess-" + connName, ConnectionID: connName, Cols: cols, Rows: rows, Active: true}, nil
}

func (f *fakeTerminal) Write(sessionID string, data []byte) error {
	f.written = append(f.written, string(data))
	return nil
}

func (f *fakeTerminal) Read(sessionID string, clear bool) (string, error) {
	return f.buf, nil
}

func (f *fakeTerminal) Close(sessionID string) error {
	f.closed++
	return nil
}

func (f *fakeTerminal) Resize(sessionID string, cols, rows int) error {
	f.resized++
	return nil
}

func TestTerminalTool_Dispatch(t *testing.T) {
	ft := &fakeTerminal{buf: "hello"}
	tt := NewTerminalTool(ft)

	cases := []struct {
		name     string
		args     map[string]any
		wantErr  bool
		wantText string
	}{
		{"open", map[string]any{"action": "open", "connection": "web"}, false, "session_id: sess-web"},
		{"open missing conn", map[string]any{"action": "open"}, true, "connection is required"},
		{"send", map[string]any{"action": "send", "session_id": "s1", "data": "ls\n"}, false, "sent"},
		{"read", map[string]any{"action": "read", "session_id": "s1"}, false, "hello"},
		{"resize", map[string]any{"action": "resize", "session_id": "s1", "cols": 120, "rows": 40}, false, "resized"},
		{"close", map[string]any{"action": "close", "session_id": "s1"}, false, "closed"},
		{"unknown", map[string]any{"action": "bogus"}, true, "unknown action"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := tt.Execute(context.Background(), c.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.IsError != c.wantErr {
				t.Fatalf("IsError=%v want %v (text=%q)", res.IsError, c.wantErr, res.Text)
			}
			if c.wantText != "" && !strings.Contains(res.Text, c.wantText) {
				t.Fatalf("text=%q want contains %q", res.Text, c.wantText)
			}
		})
	}

	if ft.opened != 1 {
		t.Errorf("OpenTerminal called %d times, want 1", ft.opened)
	}
	if len(ft.written) != 1 || ft.written[0] != "ls\n" {
		t.Errorf("Write got %v, want [ls\\n]", ft.written)
	}
	if ft.resized != 1 {
		t.Errorf("Resize called %d times, want 1", ft.resized)
	}
	if ft.closed != 1 {
		t.Errorf("Close called %d times, want 1", ft.closed)
	}
}

func TestTerminalTool_Run(t *testing.T) {
	ft := &fakeTerminal{buf: "$ ls\nfile.txt\n"}
	tt := NewTerminalTool(ft)
	res, err := tt.Execute(context.Background(), map[string]any{
		"action": "run", "connection": "db", "command": "ls", "wait_ms": 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run failed: %s", res.Text)
	}
	if !strings.Contains(res.Text, "file.txt") {
		t.Errorf("run output=%q want contains file.txt", res.Text)
	}
	if ft.opened != 1 || ft.closed != 1 {
		t.Errorf("run should open+close once, got opened=%d closed=%d", ft.opened, ft.closed)
	}
	if len(ft.written) != 1 || !strings.HasSuffix(ft.written[0], "\n") {
		t.Errorf("run should send command+newline, got %v", ft.written)
	}
}

func TestTerminalTool_SchemaValid(t *testing.T) {
	tt := NewTerminalTool(&fakeTerminal{})
	if tt.Name() != "ssh_terminal" {
		t.Errorf("name=%q", tt.Name())
	}
	schema := tt.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type=%v", schema["type"])
	}
	// Should be constructable as an ITool (compile-time ensured) and runnable.
	_ = tool.Result{}
}
