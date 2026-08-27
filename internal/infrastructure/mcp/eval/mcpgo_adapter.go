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

// ListResources implements IMCPClient.
func (m *MCPGoClient) ListResources(ctx context.Context) ([]model.ResourceDef, error) {
	res, err := m.client.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]model.ResourceDef, 0, len(res.Resources))
	for _, r := range res.Resources {
		out = append(out, model.ResourceDef{
			URI:        r.URI,
			Name:       r.Name,
			ServerName: m.name,
		})
	}
	return out, nil
}

// ReadResource implements IMCPClient.
func (m *MCPGoClient) ReadResource(ctx context.Context, uri string) (*model.ResourceContent, error) {
	req := mcp.ReadResourceRequest{}
	req.Params.URI = uri
	res, err := m.client.ReadResource(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(res.Contents) == 0 {
		return nil, fmt.Errorf("no content for resource: %s", uri)
	}
	c := res.Contents[0]
	if trc, ok := mcp.AsTextResourceContents(c); ok {
		return &model.ResourceContent{
			URI:      trc.URI,
			MimeType: trc.MIMEType,
			Text:     trc.Text,
		}, nil
	}
	return &model.ResourceContent{URI: uri}, nil
}

// ListPrompts implements IMCPClient.
func (m *MCPGoClient) ListPrompts(ctx context.Context) ([]model.PromptDef, error) {
	res, err := m.client.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]model.PromptDef, 0, len(res.Prompts))
	for _, p := range res.Prompts {
		pd := model.PromptDef{
			Name:        p.Name,
			Description: p.Description,
			ServerName:  m.name,
		}
		for _, a := range p.Arguments {
			pd.Arguments = append(pd.Arguments, model.PromptArgDef{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
			})
		}
		out = append(out, pd)
	}
	return out, nil
}

// GetPrompt implements IMCPClient.
func (m *MCPGoClient) GetPrompt(ctx context.Context, name string, args map[string]string) ([]model.PromptMessage, error) {
	req := mcp.GetPromptRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := m.client.GetPrompt(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]model.PromptMessage, 0, len(res.Messages))
	for _, msg := range res.Messages {
		content := ""
		if tc, ok := mcp.AsTextContent(msg.Content); ok {
			content = tc.Text
		}
		out = append(out, model.PromptMessage{
			Role:    string(msg.Role),
			Content: content,
		})
	}
	return out, nil
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
