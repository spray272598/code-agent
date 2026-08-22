package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
)

// buildServer returns an in-memory MCP server exposing a tool, a resource and
// a prompt — exercising the three feature families mcp-go supports.
func buildServer() *server.MCPServer {
	s := server.NewMCPServer("eval-srv", "0.1.0",
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
	)
	s.AddTool(mcp.NewTool("greet", mcp.WithDescription("greet someone"),
		mcp.WithString("name", mcp.Description("who"))),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name := req.GetString("name", "world")
			return mcp.NewToolResultText("hello " + name), nil
		})
	s.AddResource(mcp.NewResource("file:///readme", "readme",
		mcp.WithResourceDescription("project readme")),
		func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{URI: "file:///readme", Text: "README body"},
			}, nil
		})
	s.AddPrompt(mcp.NewPrompt("summarize", mcp.WithPromptDescription("summarize text")),
		func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return mcp.NewGetPromptResult("summarize this", []mcp.PromptMessage{
				mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent("text here")),
			}), nil
		})
	return s
}

// TestMCPGoImplementsIMCPClient proves mcp-go satisfies our domain port and
// that call/initialize/ping/list work end-to-end over in-process transport.
func TestMCPGoImplementsIMCPClient(t *testing.T) {
	ctx := context.Background()
	c, err := NewInProcess("eval", buildServer())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer c.Close()

	// Compile-time assertion that the adapter satisfies the production port.
	var _ port.IMCPClient = c

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "greet" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	out, err := c.CallTool(ctx, "greet", map[string]any{"name": "mcp"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != "hello mcp" {
		t.Fatalf("call result = %q", out)
	}
}

// TestMCPGoResourcesPrompts shows mcp-go covers feature families our current
// IMCPClient port does NOT expose — the evidence for the 2.4 roadmap trigger.
func TestMCPGoResourcesPrompts(t *testing.T) {
	ctx := context.Background()
	c, err := NewInProcess("eval", buildServer())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer c.Close()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	resources, err := c.ListResources(ctx)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 1 || !strings.Contains(resources[0], "readme") {
		t.Fatalf("unexpected resources: %+v", resources)
	}

	prompts, err := c.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "summarize" {
		t.Fatalf("unexpected prompts: %+v", prompts)
	}
}
