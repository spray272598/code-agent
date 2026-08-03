package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// StdioClient implements domain mcp port.IMCPClient over stdio JSON-RPC.
type StdioClient struct {
	name       string
	command    string
	args       []string
	env        map[string]string
	timeout    time.Duration
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	mu         sync.Mutex
	pending    map[int64]chan rpcResponse
	nextID     atomic.Int64
	closed     atomic.Bool
	toolsCache []model.ToolDef
}

func NewStdioClient(cfg model.ServerConfig) *StdioClient {
	to := time.Duration(cfg.TimeoutSec) * time.Second
	if to <= 0 {
		to = 60 * time.Second
	}
	return &StdioClient{
		name: cfg.Name, command: cfg.Command, args: cfg.Args, env: cfg.Env,
		timeout: to, pending: make(map[int64]chan rpcResponse),
	}
}

func (c *StdioClient) Name() string { return c.name }

func (c *StdioClient) Initialize(ctx context.Context) error {
	if c.command == "" {
		return fmt.Errorf("mcp stdio %s: empty command", c.name)
	}
	c.cmd = exec.Command(c.command, c.args...)
	if len(c.env) > 0 {
		env := os.Environ()
		for k, v := range c.env {
			env = append(env, k+"="+v)
		}
		c.cmd.Env = env
	}
	// Windows: hide console window for child MCP process
	hideMCPWindow(c.cmd)
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start mcp %s: %w", c.name, err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[mcp:%s:stderr] %s\n", c.name, sc.Text())
		}
	}()
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	go c.readLoop()

	params := initializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo:      clientInfo{Name: "code-agent", Version: "0.2.0"},
	}
	var result json.RawMessage
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		_ = c.Close()
		return fmt.Errorf("mcp initialize %s: %w", c.name, err)
	}
	_ = c.notify(ctx, "notifications/initialized", map[string]any{})
	if tools, err := c.ListTools(ctx); err == nil {
		c.toolsCache = tools
		log.Printf("[mcp] %s tools=%d\n", c.name, len(tools))
	}
	return nil
}

func (c *StdioClient) ListTools(ctx context.Context) ([]model.ToolDef, error) {
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

func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
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

func (c *StdioClient) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

func (c *StdioClient) call(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return fmt.Errorf("mcp client closed")
	}
	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	timeout := c.timeout
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 && d < timeout {
			timeout = d
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("mcp call timeout: %s", method)
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

func (c *StdioClient) notify(_ context.Context, method string, params any) error {
	return c.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *StdioClient) write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("stdin closed")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.stdin.Write(b)
	return err
}

func (c *StdioClient) readLoop() {
	for {
		if c.closed.Load() {
			return
		}
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytesTrim(line)
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.ID == nil {
			continue
		}
		id := toInt64(resp.ID)
		c.mu.Lock()
		ch := c.pending[id]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- resp:
			default:
			}
		}
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	default:
		var n int64
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &n)
		return n
	}
}

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
		b = b[:len(b)-1]
	}
	return b
}
