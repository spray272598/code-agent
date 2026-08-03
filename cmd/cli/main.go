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

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "server base URL")
	apiKey := flag.String("key", envOr("CODE_AGENT_API_KEY", "dev-key"), "API key")
	user := flag.String("user", "cli-user", "user id")
	stream := flag.Bool("stream", true, "use SSE stream")
	autoApprove := flag.Bool("auto-approve", false, "auto approve write/bash")
	flag.Parse()

	sessionID := ""
	// create session
	sess, err := postJSON(*base+"/api/v1/session", *apiKey, map[string]any{
		"userId": *user, "title": "cli",
	})
	if err == nil {
		if d, ok := sess["data"].(map[string]any); ok {
			sessionID, _ = d["sessionId"].(string)
		}
	}
	fmt.Printf("code-agent CLI  base=%s  session=%s\n", *base, sessionID)
	fmt.Println("type message; /quit exit; /continue after approve; /help /tools /skills /mcp on server")
	fmt.Println("---")

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			break
		}
		if line == "/continue" {
			line = "继续"
		}
		if line == "/tools" {
			body, err := getJSON(*base+"/api/v1/tools", *apiKey)
			if err != nil {
				fmt.Println("err:", err)
				continue
			}
			pretty, _ := json.MarshalIndent(body["data"], "", "  ")
			fmt.Println(string(pretty))
			continue
		}

		req := map[string]any{
			"sessionId": sessionID, "userId": *user,
			"message": line, "autoApprove": *autoApprove,
		}
		if *stream {
			if err := streamChat(*base+"/api/v1/chat/stream", *apiKey, req, &sessionID); err != nil {
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
					fmt.Println("[permission required — approve via API then /continue]")
				}
			}
		}
		fmt.Println()
	}
}

func streamChat(url, key string, req map[string]any, sessionID *string) error {
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
			handleEvent(event, data, sessionID)
			event = ""
		}
	}
	return sc.Err()
}

func handleEvent(event, data string, sessionID *string) {
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
	case "tool_call":
		fmt.Printf("\n⚙ tool %v %v\n", m["subType"], m["data"])
	case "tool_result":
		if c, ok := m["content"].(string); ok {
			fmt.Printf("↳ %s\n", trim(c, 300))
		}
	case "permission":
		fmt.Printf("\n⚠ %v\n", m["content"])
	case "compress":
		fmt.Printf("\n📦 %v\n", m["content"])
	case "answer":
		// already streamed via text_delta often
		if c, ok := m["content"].(string); ok && c != "" {
			// if no deltas were printed, show answer
			_ = c
		}
		fmt.Println()
	case "error":
		fmt.Printf("\n✖ %v\n", m["content"])
	case "done":
		fmt.Println()
	default:
		// ignore
	}
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
