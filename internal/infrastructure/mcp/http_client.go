package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/types/common"
)

// HTTPClient implements the MCP Streamable HTTP transport (formerly SSE). It is
// used for remote / third-party MCP servers already running behind an HTTP URL,
// in contrast to StdioClient which spawns a local child process.
//
// Transport model: every JSON-RPC message is a POST to the endpoint URL. The
// response may be a single JSON body (application/json) or an SSE event stream
// (text/event-stream). Server-initiated notifications are not consumed (this
// agent does not act as an MCP server), so only request/response is handled.
type HTTPClient struct {
	name       string
	url        string
	headers    map[string]string
	timeout    time.Duration
	client     *http.Client
	mu         sync.Mutex
	sessionID  string
	nextID     atomic.Int64
	closed     atomic.Bool
	toolsCache []model.ToolDef
}

func NewHTTPClient(cfg model.ServerConfig) *HTTPClient {
	to := time.Duration(cfg.TimeoutSec) * time.Second
	if to <= 0 {
		to = 60 * time.Second
	}
	return &HTTPClient{
		name:    cfg.Name,
		url:     cfg.URL,
		headers: cfg.Env, // reuse Env for extra headers (e.g. Authorization)
		timeout: to,
		client:  &http.Client{Timeout: to},
	}
}

func (c *HTTPClient) Name() string { return c.name }

func (c *HTTPClient) Initialize(ctx context.Context) error {
	if c.url == "" {
		return fmt.Errorf("mcp http %s: empty url", c.name)
	}
	params := initializeParams{
		ProtocolVersion: "2025-06-18",
		Capabilities:    map[string]any{},
		ClientInfo:      clientInfo{Name: "code-agent", Version: "0.3.0"},
	}
	var result json.RawMessage
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return fmt.Errorf("mcp initialize %s: %w", c.name, err)
	}
	_ = c.notify(ctx, "notifications/initialized", map[string]any{})
	if tools, err := c.ListTools(ctx); err == nil {
		c.toolsCache = tools
		log.Printf("[mcp] %s tools=%d\n", c.name, len(tools))
	}
	return nil
}

func (c *HTTPClient) Ping(ctx context.Context) error {
	var result json.RawMessage
	return c.call(ctx, "ping", map[string]any{}, &result)
}

func (c *HTTPClient) ListTools(ctx context.Context) ([]model.ToolDef, error) {
	var res toolsListResult
	if err := c.call(ctx, "tools/list", map[string]any{}, &res); err != nil {
		if len(c.toolsCache) > 0 {
			return c.toolsCache, nil
		}
		return nil, err
	}
	out := make([]model.ToolDef, 0, len(res.Tools))
	for _, t := range res.Tools {
		out = append(out, model.ToolDef{
			Name: t.Name, Description: t.Description,
			InputSchema: t.InputSchema, ServerName: c.name,
		})
	}
	c.toolsCache = out
	return out, nil
}

func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	var res toolsCallResult
	if err := c.call(ctx, "tools/call", toolsCallParams{Name: name, Arguments: args}, &res); err != nil {
		return "", err
	}
	text := extractText(&res)
	if res.IsError {
		return text, fmt.Errorf("mcp tool error: %s", text)
	}
	return text, nil
}

func (c *HTTPClient) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	// best-effort session teardown
	if c.sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
		req.Header.Set("Mcp-Session-Id", c.sessionID)
		_, _ = c.client.Do(req)
		cancel()
	}
	return nil
}

func (c *HTTPClient) ListResources(ctx context.Context) ([]model.ResourceDef, error) {
	var res struct {
		Resources []struct {
			URI         string `json:"uri"`
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			MimeType    string `json:"mimeType,omitempty"`
		} `json:"resources"`
	}
	if err := c.call(ctx, "resources/list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	out := make([]model.ResourceDef, 0, len(res.Resources))
	for _, r := range res.Resources {
		out = append(out, model.ResourceDef{
			URI: r.URI, Name: r.Name, Description: r.Description,
			MimeType: r.MimeType, ServerName: c.name,
		})
	}
	return out, nil
}

func (c *HTTPClient) ReadResource(ctx context.Context, uri string) (*model.ResourceContent, error) {
	var res model.ResourceContent
	if err := c.call(ctx, "resources/read", map[string]any{"uri": uri}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *HTTPClient) ListPrompts(ctx context.Context) ([]model.PromptDef, error) {
	var res struct {
		Prompts []struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Arguments   []struct {
				Name        string `json:"name"`
				Description string `json:"description,omitempty"`
				Required    bool   `json:"required,omitempty"`
			} `json:"arguments,omitempty"`
		} `json:"prompts"`
	}
	if err := c.call(ctx, "prompts/list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	out := make([]model.PromptDef, 0, len(res.Prompts))
	for _, p := range res.Prompts {
		pd := model.PromptDef{
			Name: p.Name, Description: p.Description, ServerName: c.name,
		}
		for _, a := range p.Arguments {
			pd.Arguments = append(pd.Arguments, model.PromptArgDef{
				Name: a.Name, Description: a.Description, Required: a.Required,
			})
		}
		out = append(out, pd)
	}
	return out, nil
}

func (c *HTTPClient) GetPrompt(ctx context.Context, name string, args map[string]string) ([]model.PromptMessage, error) {
	var res struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := c.call(ctx, "prompts/get", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return nil, err
	}
	out := make([]model.PromptMessage, 0, len(res.Messages))
	for _, m := range res.Messages {
		out = append(out, model.PromptMessage{Role: m.Role, Content: m.Content})
	}
	return out, nil
}

func (c *HTTPClient) call(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp client closed")
	}
	id := c.nextID.Add(1)
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Unlock()

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// capture session id for subsequent requests
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mcp http %d: %s", resp.StatusCode, common.TruncateRunes(string(raw), 300))
	}

	// unwrap SSE stream if the server responds with an event stream
	var payload []byte
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		payload = extractSSEData(raw)
	} else {
		payload = raw
	}
	if len(payload) == 0 {
		return nil
	}

	var rpc rpcResponse
	if err := json.Unmarshal(payload, &rpc); err != nil {
		// some servers return the result directly (no jsonrpc envelope)
		if result != nil {
			if err2 := json.Unmarshal(payload, result); err2 == nil {
				return nil
			}
		}
		return fmt.Errorf("mcp http parse: %w", err)
	}
	if rpc.Error != nil {
		return rpc.Error
	}
	if result != nil && len(rpc.Result) > 0 {
		return json.Unmarshal(rpc.Result, result)
	}
	return nil
}

func (c *HTTPClient) notify(ctx context.Context, method string, params any) error {
	var ignored json.RawMessage
	return c.call(ctx, method, params, &ignored)
}

// extractSSEData pulls the data: payload from a text/event-stream body. It
// returns the first non-empty data line, which for request/response SSE carries
// the JSON-RPC response.
func extractSSEData(body []byte) []byte {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if d, ok := strings.CutPrefix(line, "data:"); ok {
			d = strings.TrimSpace(d)
			if d != "" && d != "[DONE]" {
				return []byte(d)
			}
		}
	}
	return nil
}
