package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var slashCmds = []string{
	"/help", "/quit", "/exit", "/continue", "/tools", "/skills", "/mcp",
	"/clear", "/compact", "/pending", "/approve", "/reject", "/memory", "/metrics",
	"/status", "/team", "/deep", "/cost", "/plan", "/plan-implement",
}

func main() {
	base := flag.String("base", envOr("CODE_AGENT_BASE", "http://127.0.0.1:8080"), "server base URL")
	apiKey := flag.String("key", envOr("CODE_AGENT_API_KEY", "dev-key"), "API key")
	user := flag.String("user", envOr("CODE_AGENT_USER", "cli-user"), "user id")
	stream := flag.Bool("stream", true, "use SSE stream")
	autoApprove := flag.Bool("auto-approve", false, "auto approve write/bash")
	quiet := flag.Bool("quiet", false, "less chrome on startup")
	flag.Parse()

	// Health probe with friendly error (ease of use)
	if err := waitHealth(*base, *apiKey, 3); err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach server %s: %v\n", *base, err)
		fmt.Fprintf(os.Stderr, "start:  go run ./cmd/server -config configs/config.yaml\n")
		fmt.Fprintf(os.Stderr, "or:     powershell -File scripts/try_cli.ps1\n")
		os.Exit(1)
	}

	sessionID := ""
	sess, err := postJSON(*base+"/api/v1/session", *apiKey, map[string]any{
		"userId": *user, "title": "cli",
	})
	if err == nil {
		if d, ok := sess["data"].(map[string]any); ok {
			sessionID, _ = d["sessionId"].(string)
		}
	}
	if !*quiet {
		fmt.Println("┌─ code-agent CLI ─────────────────────────────────")
		fmt.Printf("│ base=%s\n", *base)
		fmt.Printf("│ session=%s  user=%s  stream=%v  autoApprove=%v\n", sessionID, *user, *stream, *autoApprove)
		fmt.Println("│ Eino ReAct + GuardedTool  |  HITL: /pending → y  |  /help")
		fmt.Println("│ tips: /team <goal>  ·  /deep <goal>  ·  /status  ·  /prefix?")
		fmt.Println("└──────────────────────────────────────────────────")
	} else {
		fmt.Printf("cli session=%s\n", sessionID)
	}

	lastPermID := ""
	in := bufio.NewScanner(os.Stdin)
	// larger line buffer for pasting patches
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("> ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") && strings.HasSuffix(line, "?") {
			matches := completeSlash(strings.TrimSuffix(line, "?"))
			if len(matches) == 0 {
				fmt.Println("(no matches)")
			} else if len(matches) == 1 {
				fmt.Println("→", matches[0])
			} else {
				fmt.Println("completions:", strings.Join(matches, "  "))
			}
			continue
		}
		if line == "/quit" || line == "/exit" {
			break
		}
		if line == "/continue" {
			line = "继续"
		}
		if line == "/tools" {
			printJSON(getJSON(*base+"/api/v1/tools", *apiKey))
			continue
		}
		if line == "/pending" {
			body, err := getJSON(*base+"/api/v1/permission/pending?sessionId="+sessionID, *apiKey)
			if err != nil {
				fmt.Println("err:", err)
				continue
			}
			pretty, _ := json.MarshalIndent(body["data"], "", "  ")
			fmt.Println(string(pretty))
			// remember first pending id
			if arr, ok := body["data"].([]any); ok && len(arr) > 0 {
				if m, ok := arr[0].(map[string]any); ok {
					if id, _ := m["id"].(string); id != "" {
						lastPermID = id
						fmt.Println("(tip) /approve  or  /approve", id, "once")
					}
				}
			}
			continue
		}
		if strings.HasPrefix(line, "/approve") {
			parts := strings.Fields(line)
			id, scope := lastPermID, "once"
			if len(parts) >= 2 {
				id = parts[1]
			}
			if len(parts) >= 3 {
				scope = parts[2]
			}
			if id == "" {
				fmt.Println("usage: /approve [permId] [once|session]  (or /pending first)")
				continue
			}
			body, err := postJSON(*base+"/api/v1/permission/approve", *apiKey, map[string]any{
				"id": id, "scope": scope, "continue": true,
				"sessionId": sessionID, "userId": *user,
			})
			if err != nil {
				fmt.Println("err:", err)
				continue
			}
			if d, ok := body["data"].(map[string]any); ok {
				if chat, ok := d["chat"].(map[string]any); ok {
					fmt.Println(chat["response"])
				} else {
					pretty, _ := json.MarshalIndent(d, "", "  ")
					fmt.Println(string(pretty))
				}
			}
			lastPermID = ""
			continue
		}
		if strings.HasPrefix(line, "/reject") {
			parts := strings.Fields(line)
			id := lastPermID
			if len(parts) >= 2 {
				id = parts[1]
			}
			if id == "" {
				fmt.Println("usage: /reject [permId]")
				continue
			}
			_, err := postJSON(*base+"/api/v1/permission/reject", *apiKey, map[string]any{"id": id})
			if err != nil {
				fmt.Println("err:", err)
			} else {
				fmt.Println("rejected", id)
				lastPermID = ""
			}
			continue
		}
		if line == "/help" {
			fmt.Println(`Slash commands:
  /help                         this text
  /status                       session + health + metrics snapshot
  /pending                      list HITL confirmations
  /approve [id] [once|session]  approve + inline continue
  /reject [id]                  reject pending
  y / yes / /continue           resume after approve
  /tools /skills /mcp           inventory
  /memory /metrics /cost        memory & usage
  /compact /clear               context
  /team <goal>                  multi-agent explore+verify (eino)
  /deep <goal>                  sequential Plan→Act→Reflect
  /quit                         exit
Tips: paste multi-line carefully; after CONFIRM type y
Complete: type /pre?  e.g. /ap? → /approve`)
			continue
		}
		if line == "/status" {
			printStatus(*base, *apiKey, sessionID)
			continue
		}
		if line == "/plan" {
			if sessionID == "" {
				fmt.Println("start a session first (send a message)")
				continue
			}
			if _, err := postJSON(*base+"/api/v1/session/plan/explore", *apiKey, map[string]any{"sessionId": sessionID}); err != nil {
				fmt.Println("err:", err)
			} else {
				fmt.Println("[plan] explore phase: read-only, mutating tools blocked")
			}
			continue
		}
		if line == "/plan-implement" {
			if sessionID == "" {
				fmt.Println("no active session")
				continue
			}
			if _, err := postJSON(*base+"/api/v1/session/plan/implement", *apiKey, map[string]any{"sessionId": sessionID}); err != nil {
				fmt.Println("err:", err)
			} else {
				fmt.Println("[plan] implement phase: writable")
			}
			continue
		}
		if strings.HasPrefix(line, "/team ") || line == "/team" {
			goal := strings.TrimSpace(strings.TrimPrefix(line, "/team"))
			if goal == "" {
				fmt.Println("usage: /team <goal>   e.g. /team review auth middleware")
				continue
			}
			line = "/team " + goal
		}
		if strings.HasPrefix(line, "/deep ") || line == "/deep" {
			goal := strings.TrimSpace(strings.TrimPrefix(line, "/deep"))
			if goal == "" {
				fmt.Println("usage: /deep <goal>")
				continue
			}
			line = "/deep " + goal
		}

		// interactive short confirm
		if line == "y" || line == "Y" || line == "yes" {
			if lastPermID != "" {
				body, err := postJSON(*base+"/api/v1/permission/approve", *apiKey, map[string]any{
					"id": lastPermID, "scope": "once", "continue": true,
					"sessionId": sessionID, "userId": *user,
				})
				if err != nil {
					fmt.Println("err:", err)
					continue
				}
				if d, ok := body["data"].(map[string]any); ok {
					if chat, ok := d["chat"].(map[string]any); ok {
						fmt.Println(chat["response"])
					}
				}
				lastPermID = ""
				continue
			}
			line = "继续"
		}

		req := map[string]any{
			"sessionId": sessionID, "userId": *user,
			"message": line, "autoApprove": *autoApprove,
		}
		if *stream {
			if err := streamChat(*base+"/api/v1/chat/stream", *apiKey, req, &sessionID, &lastPermID); err != nil {
				fmt.Println("err:", err)
			}
		} else {
			body, err := postJSON(*base+"/api/v1/chat", *apiKey, req)
			if err != nil {
				fmt.Println("err:", err)
				continue
			}
			if d, ok := body["data"].(map[string]any); ok {
				if sid, _ := d["sessionId"].(string); sid != "" {
					sessionID = sid
				}
				fmt.Println(d["response"])
				if d["needPermission"] == true {
					fmt.Println("[permission] /pending then /approve  or  y")
					if p, ok := d["pendingPermission"].(map[string]any); ok {
						if id, _ := p["id"].(string); id != "" {
							lastPermID = id
						}
					}
				}
			}
		}
		fmt.Println()
	}
}

func completeSlash(prefix string) []string {
	prefix = strings.ToLower(prefix)
	var out []string
	for _, c := range slashCmds {
		if strings.HasPrefix(strings.ToLower(c), prefix) {
			out = append(out, c)
		}
	}
	return out
}

func streamChat(url, key string, req map[string]any, sessionID, lastPerm *string) error {
	raw, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", key)
	httpReq.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(b))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var event string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			handleEvent(event, data, sessionID, lastPerm)
			event = ""
		}
	}
	return sc.Err()
}

func handleEvent(event, data string, sessionID, lastPerm *string) {
	var m map[string]any
	_ = json.Unmarshal([]byte(data), &m)
	switch event {
	case "session":
		if sid, ok := m["sessionId"].(string); ok && sid != "" {
			*sessionID = sid
		}
	case "text_delta":
		if c, ok := m["content"].(string); ok {
			fmt.Print(c)
		}
	case "thought":
		if c, ok := m["content"].(string); ok {
			fmt.Printf("\n· %s\n", c)
		}
	case "observation", "action":
		if c, ok := m["content"].(string); ok {
			fmt.Printf("\n○ %s %s\n", event, trim(c, 120))
		}
	case "tool_call":
		fmt.Printf("\n⚙ tool %v %v\n", m["subType"], m["data"])
	case "tool_result":
		if c, ok := m["content"].(string); ok {
			fmt.Printf("↳ %s\n", trim(c, 300))
		}
	case "permission":
		fmt.Printf("\n⚠ %v\n", m["content"])
		fmt.Println("   → type y  or  /approve   after /pending")
		if d, ok := m["data"].(map[string]any); ok {
			if id, _ := d["id"].(string); id != "" {
				*lastPerm = id
			}
		}
	case "compress":
		fmt.Printf("\n📦 %v\n", m["content"])
	case "subagent":
		fmt.Printf("\n👥 %v %v\n", m["subType"], trim(fmt.Sprint(m["content"]), 100))
	case "answer":
		fmt.Println()
	case "error":
		fmt.Printf("\n✖ %v\n", m["content"])
	case "done":
		if d, ok := m["data"].(map[string]any); ok {
			if tc, ok := d["toolCalls"]; ok {
				fmt.Printf("\n✓ done  tools=%v  tokens~%v  orch=%v\n", tc, d["tokenEst"], d["orchestrator"])
			} else {
				fmt.Println()
			}
		} else {
			fmt.Println()
		}
	case "resume":
		fmt.Printf("\n↻ resume %v %v\n", m["subType"], trim(fmt.Sprint(m["content"]), 80))
	case "checkpoint":
		fmt.Printf("\n💾 checkpoint %v\n", m["subType"])
	}
}

func waitHealth(base, key string, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/health", nil)
		if key != "" {
			req.Header.Set("X-API-Key", key)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			last = fmt.Errorf("http %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(400 * time.Millisecond)
	}
	return last
}

func printStatus(base, key, sessionID string) {
	fmt.Printf("session=%s\n", sessionID)
	if body, err := getJSON(base+"/health", key); err == nil {
		pretty, _ := json.MarshalIndent(body, "", "  ")
		fmt.Println("health:", string(pretty))
	} else {
		fmt.Println("health err:", err)
	}
	// usage panel (3.5): token/quota/cost breakdown
	usageURL := base + "/api/v1/usage"
	if sessionID != "" {
		usageURL += "?session=" + sessionID
	}
	if body, err := getJSON(usageURL, key); err == nil {
		if d, ok := body["data"].(map[string]any); ok {
			pretty, _ := json.MarshalIndent(d, "", "  ")
			fmt.Println("usage:", string(pretty))
		}
	} else {
		fmt.Println("usage err:", err)
	}
	if sessionID != "" {
		if body, err := getJSON(base+"/api/v1/permission/pending?sessionId="+sessionID, key); err == nil {
			pretty, _ := json.MarshalIndent(body["data"], "", "  ")
			fmt.Println("pending:", string(pretty))
		}
	}
}

func printJSON(body map[string]any, err error) {
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	pretty, _ := json.MarshalIndent(body["data"], "", "  ")
	fmt.Println(string(pretty))
}

func postJSON(url, key string, body any) (map[string]any, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s", string(b))
	}
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("http %d: %s", resp.StatusCode, string(b))
	}
	return out, nil
}

func getJSON(url, key string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
