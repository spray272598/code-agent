package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spray272598/code-agent/internal/domain/host"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
)

// host-agent: runs on developer machine, executes tools locally for the server.
//
//	go run ./cmd/host-agent --server ws://127.0.0.1:8080/ws/host --token dev-key --workspace .
func main() {
	server := flag.String("server", "ws://127.0.0.1:8080/ws/host", "server host websocket URL")
	token := flag.String("token", env("CODE_AGENT_API_KEY", "dev-key"), "API token")
	deviceID := flag.String("device", "local-dev", "device id")
	workspace := flag.String("workspace", ".", "local workspace root")
	flag.Parse()

	abs, err := filepath.Abs(*workspace)
	if err != nil {
		log.Fatal(err)
	}
	ws := coding.NewWorkspace(abs)
	tools := map[string]tool.ITool{
		"read_file":  coding.NewReadFile(ws),
		"write_file": coding.NewWriteFile(ws),
		"edit_file":  coding.NewEditFile(ws),
		"bash":       coding.NewBash(ws, 60),
		"glob":       coding.NewGlob(ws),
		"grep":       coding.NewGrep(ws),
	}

	u, err := url.Parse(*server)
	if err != nil {
		log.Fatal(err)
	}
	q := u.Query()
	q.Set("token", *token)
	q.Set("deviceId", *deviceID)
	u.RawQuery = q.Encode()

	log.Printf("[host-agent] connecting %s workspace=%s\n", u.String(), abs)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// hello
	_ = conn.WriteJSON(host.Envelope{
		Type: host.MsgHello, DeviceID: *deviceID, Workspace: abs,
	})

	// graceful
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		_ = conn.Close()
		os.Exit(0)
	}()

	// ping ticker
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			_ = conn.WriteJSON(host.Envelope{Type: host.MsgPing, DeviceID: *deviceID})
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[host-agent] disconnected: %v\n", err)
			return
		}
		var env host.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Type {
		case host.MsgToolCall:
			go handleTool(conn, tools, env)
		case host.MsgPing:
			_ = conn.WriteJSON(host.Envelope{Type: host.MsgPong, DeviceID: *deviceID})
		case host.MsgPong:
		default:
			log.Printf("[host-agent] msg type=%s\n", env.Type)
		}
	}
}

func handleTool(conn *websocket.Conn, tools map[string]tool.ITool, env host.Envelope) {
	t := tools[env.Name]
	out := host.Envelope{
		Type: host.MsgToolResult, CallID: env.CallID, Name: env.Name, DeviceID: env.DeviceID,
	}
	if t == nil {
		out.OK = false
		out.Error = "unknown tool: " + env.Name
		out.Text = out.Error
	} else {
		res, err := t.Execute(context.Background(), env.Args)
		if err != nil {
			out.OK = false
			out.Error = err.Error()
			out.Text = err.Error()
		} else {
			out.OK = !res.IsError
			out.Text = res.Text
			if res.IsError {
				out.Error = res.Text
			}
		}
	}
	if err := conn.WriteJSON(out); err != nil {
		log.Printf("[host-agent] write result: %v\n", err)
	} else {
		log.Printf("[host-agent] tool=%s callId=%s ok=%v\n", env.Name, env.CallID, out.OK)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// silence unused
var _ = fmt.Sprintf
