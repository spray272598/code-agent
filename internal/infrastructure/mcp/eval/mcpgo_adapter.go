// Package eval contains a feasibility spike for M2 / 2.4b: evaluating the
// mark3labs/mcp-go SDK as a drop-in implementation of our domain IMCPClient
// port. It is NOT wired into production code paths (the hand-rolled
// cmd-based client remains the default). Its purpose is to (a) prove mcp-go
// satisfies IMCPClient and (b) surface which MCP features our current port
// does NOT expose (resources, prompts, sampling) so the roadmap trigger
// condition can be decided with evidence.
package eval

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgoclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// MCPGoClient adapts mark3labs/mcp-go to our domain IMCPClient port.
type MCPGoClient struct {
	name   string
	client *mcpgoclient.Client
}

// NewStdio builds a client that talks to a server launched via stdio.
func NewStdio(name, command string, args []string, env []string) (*MCPGoClient, error) {
	stdio := transport.NewStdio(command, env, args...)
	c := mcpgoclient.NewClient(stdio)
	return wrap(name, c)
}

// NewInProcess builds a client backed by an in-memory server (used by tests).
func NewInProcess(name string, srv *server.MCPServer) (*MCPGoClient, error) {
	t := transport.NewInProcessTransport(srv)
	c := mcpgoclient.NewClient(t)
	return wrap(name, c)
}

func wrap(name string, c *mcpgoclient.Client) (*MCPGoClient, error) {
	if c == nil {
		return nil, fmt.Errorf("nil mcp-go client")
	}
	return &MCPGoClient{name: name, client: c}, nil
}

// Name implements IMCPClient.
func (m *MCPGoClient) Name() string { return m.name }

// Initialize implements IMCPClient: starts the transport and performs the
// MCP handshake. mcp-go requires Start before Initialize.
func (m *MCPGoClient) Initialize(ctx context.Context) error {
	if err := m.client.Start(ctx); err != nil {
		return err
	}
	_, err := m.client.Initialize(ctx, mcp.InitializeRequest{})
	return err
}

// Ping implements IMCPClient.
func (m *MCPGoClient) Ping(ctx context.Context) error {
	return m.client.Ping(ctx)
}

// ListTools implements IMCPClient.
func (m *MCPGoClient) ListTools(ctx context.Context) ([]model.ToolDef, error) {
	res, err := m.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]model.ToolDef, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := map[string]any{}
		if b, e := jsonMarshalMap(t.InputSchema); e == nil {
			schema = b
		}
		out = append(out, model.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			ServerName:  m.name,
		})
	}
	return out, nil
}

// CallTool implements IMCPClient.
func (m *MCPGoClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := m.client.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return contentText(res), fmt.Errorf("tool %q returned error", name)
	}
	return contentText(res), nil
}

// Close implements IMCPClient.
func (m *MCPGoClient) Close() error { return m.client.Close() }

// --- Beyond IMCPClient: features our current port lacks (eval evidence) ---

// ListResources shows mcp-go supports MCP resources (not in IMCPClient).
func (m *MCPGoClient) ListResources(ctx context.Context) ([]string, error) {
	res, err := m.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Resources))
	for _, r := range res.Resources {
		names = append(names, r.URI)
	}
	return names, nil
}

// ListPrompts shows mcp-go supports MCP prompts (not in IMCPClient).
func (m *MCPGoClient) ListPrompts(ctx context.Context) ([]string, error) {
	res, err := m.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Prompts))
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	return names, nil
}

func contentText(res *mcp.CallToolResult) string {
	var s string
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			s += t.Text
		}
	}
	return s
}

func jsonMarshalMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
