package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Minimal MCP stdio server for local integration tests.
// Tools: get_time, echo, workspace_info

type req struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type resp struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r req
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.ID == nil && strings.HasPrefix(r.Method, "notifications/") {
			continue
		}
		out := handle(r)
		if out == nil {
			continue
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	}
}

func handle(r req) *resp {
	switch r.Method {
	case "initialize":
		return &resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "code-agent-demo-mcp", "version": "1.0.0"},
		}}
	case "tools/list":
		return &resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{
			"tools": []map[string]any{
				{"name": "get_time", "description": "Return current server time", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
				{"name": "echo", "description": "Echo a message", "inputSchema": map[string]any{
					"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}, "required": []string{"message"},
				}},
				{"name": "workspace_info", "description": "Demo workspace info", "inputSchema": map[string]any{"type": "object"}},
			},
		}}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(r.Params, &p)
		text := ""
		switch p.Name {
		case "get_time":
			text = time.Now().Format(time.RFC3339)
		case "echo":
			if p.Arguments != nil {
				text = fmt.Sprint(p.Arguments["message"])
			}
		case "workspace_info":
			text = "code-agent demo mcp: ok"
		default:
			return &resp{JSONRPC: "2.0", ID: r.ID, Error: &rpcErr{Code: -32601, Message: "unknown tool"}}
		}
		return &resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": false,
		}}
	case "ping":
		return &resp{JSONRPC: "2.0", ID: r.ID, Result: map[string]any{}}
	default:
		if r.ID == nil {
			return nil
		}
		return &resp{JSONRPC: "2.0", ID: r.ID, Error: &rpcErr{Code: -32601, Message: "method not found"}}
	}
}
