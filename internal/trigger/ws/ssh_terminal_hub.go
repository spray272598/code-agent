package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
)

// SSHTerminalHub 把 WebSocket 代理到远程 SSH 的交互式 PTY 会话，实现实时终端。
// 鉴权复用与 HostHub 相同的方式（token 查询参数 / X-API-Key / Authorization: Bearer）。
// 查询参数：connection（必填）、cols、rows。
type SSHTerminalHub struct {
	Terminal sshport.ITerminal
	Auth     func(token string) bool
}

func NewSSHTerminalHub(term sshport.ITerminal, auth func(token string) bool) *SSHTerminalHub {
	return &SSHTerminalHub{Terminal: term, Auth: auth}
}

func (h *SSHTerminalHub) authToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if t := r.Header.Get("X-API-Key"); t != "" {
		return t
	}
	if a := r.Header.Get("Authorization"); len(a) > 7 && a[:7] == "Bearer " {
		return a[7:]
	}
	return ""
}

func (h *SSHTerminalHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Auth != nil && !h.Auth(h.authToken(r)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	connName := r.URL.Query().Get("connection")
	if connName == "" {
		http.Error(w, "connection required", http.StatusBadRequest)
		return
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	if cols <= 0 {
		cols = 80
	}
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if rows <= 0 {
		rows = 24
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ssh-ws] upgrade: %v\n", err)
		return
	}
	defer conn.Close()

	sess, err := h.Terminal.OpenTerminal(connName, cols, rows)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	defer h.Terminal.Close(sess.ID)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// SSH 输出 -> WS：轮询清空缓冲，仅转发增量
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				out, err := h.Terminal.Read(sess.ID, true)
				if err != nil {
					return
				}
				if out == "" {
					continue
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(out)); err != nil {
					return
				}
			}
		}
	}()

	// WS -> SSH：原始输入写入 PTY；resize 消息调整窗口
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
			_ = h.Terminal.Resize(sess.ID, msg.Cols, msg.Rows)
			continue
		}
		if err := h.Terminal.Write(sess.ID, data); err != nil {
			break
		}
	}
	close(stop)
	wg.Wait()
}
