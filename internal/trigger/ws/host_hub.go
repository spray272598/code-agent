package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spray272598/code-agent/internal/domain/host"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HostHub serves WebSocket connections from host-agents.
type HostHub struct {
	Bridge *host.Bridge
	APIKey string
}

func NewHostHub(bridge *host.Bridge, apiKey string) *HostHub {
	return &HostHub{Bridge: bridge, APIKey: apiKey}
}

func (h *HostHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// auth: query token or header
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-API-Key")
	}
	if h.APIKey != "" && token != h.APIKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	deviceID := r.URL.Query().Get("deviceId")
	if deviceID == "" {
		deviceID = "default-host"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[host-ws] upgrade: %v\n", err)
		return
	}
	defer conn.Close()

	var writeMu sync.Mutex
	send := func(env host.Envelope) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		return conn.WriteJSON(env)
	}

	sess := &host.HostSession{
		DeviceID: deviceID,
		Send:     send,
		LastSeen: time.Now(),
	}
	h.Bridge.Register(sess)
	log.Printf("[host-ws] connected device=%s\n", deviceID)
	defer func() {
		h.Bridge.Unregister(deviceID)
		log.Printf("[host-ws] disconnected device=%s\n", deviceID)
	}()

	// request hello / welcome
	_ = send(host.Envelope{Type: host.MsgPing, DeviceID: deviceID})

	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env host.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		h.Bridge.Touch(deviceID)
		switch env.Type {
		case host.MsgHello:
			sess.Workspace = env.Workspace
			if env.DeviceID != "" {
				// re-key if hello provides id
			}
			log.Printf("[host-ws] hello device=%s workspace=%s\n", deviceID, env.Workspace)
			_ = send(host.Envelope{Type: host.MsgPong, DeviceID: deviceID})
		case host.MsgToolResult:
			h.Bridge.ResolveResult(env)
		case host.MsgPing:
			_ = send(host.Envelope{Type: host.MsgPong, DeviceID: deviceID})
		case host.MsgPong:
			// ok
		default:
			log.Printf("[host-ws] unknown type=%s\n", env.Type)
		}
	}
}
