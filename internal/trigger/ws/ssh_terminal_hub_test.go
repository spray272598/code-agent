package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	sshmodel "github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
)

type fakeTerm struct {
	mu      sync.Mutex
	buf     string
	writes  []string
	resized bool
	opened  bool
	closed  bool
}

func (f *fakeTerm) OpenTerminal(connName string, cols, rows int) (*sshmodel.TerminalSession, error) {
	f.mu.Lock()
	f.opened = true
	f.mu.Unlock()
	return &sshmodel.TerminalSession{ID: "s1", ConnectionID: connName, Cols: cols, Rows: rows, Active: true}, nil
}

func (f *fakeTerm) Write(sessionID string, data []byte) error {
	f.mu.Lock()
	f.writes = append(f.writes, string(data))
	f.mu.Unlock()
	return nil
}

func (f *fakeTerm) Read(sessionID string, clear bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.buf
	if clear {
		f.buf = ""
	}
	return out, nil
}

func (f *fakeTerm) Close(sessionID string) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeTerm) Resize(sessionID string, cols, rows int) error {
	f.mu.Lock()
	f.resized = true
	f.mu.Unlock()
	return nil
}

func wsURL(base, token, conn string) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/ws/ssh?token=" + token + "&connection=" + conn
}

func TestSSHTerminalHub_Proxy(t *testing.T) {
	ft := &fakeTerm{}
	hub := NewSSHTerminalHub(ft, func(token string) bool { return token == "good" })
	srv := httptest.NewServer(hub)
	defer srv.Close()

	cli, _, err := gws.DefaultDialer.Dial(wsURL(srv.URL, "good", "web"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	if !ft.opened {
		t.Fatal("terminal not opened")
	}

	if err := cli.WriteMessage(gws.TextMessage, []byte("ls\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 模拟远端 SSH 回显
	ft.mu.Lock()
	ft.buf = "file.txt\n"
	ft.mu.Unlock()

	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := cli.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "file.txt") {
		t.Fatalf("did not receive proxied output: %q", data)
	}

	// resize 消息应转发到 Resize
	if err := cli.WriteMessage(gws.TextMessage, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	ft.mu.Lock()
	resized := ft.resized
	writes := append([]string{}, ft.writes...)
	ft.mu.Unlock()
	if !resized {
		t.Fatal("resize not forwarded to terminal")
	}
	if len(writes) != 1 || writes[0] != "ls\n" {
		t.Fatalf("input not written to terminal: %v", writes)
	}
}

func TestSSHTerminalHub_AuthRequired(t *testing.T) {
	hub := NewSSHTerminalHub(&fakeTerm{}, func(token string) bool { return false })
	srv := httptest.NewServer(hub)
	defer srv.Close()

	_, _, err := gws.DefaultDialer.Dial(wsURL(srv.URL, "bad", "web"), nil)
	if err == nil {
		t.Fatal("expected auth failure (401) before upgrade")
	}
}

func TestSSHTerminalHub_ConnectionRequired(t *testing.T) {
	hub := NewSSHTerminalHub(&fakeTerm{}, func(token string) bool { return true })
	srv := httptest.NewServer(hub)
	defer srv.Close()

	_, resp, err := gws.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/ssh?token=good", nil)
	if err == nil {
		t.Fatal("expected 400 when connection missing")
	}
	if resp != nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

var _ sshport.ITerminal = (*fakeTerm)(nil)
