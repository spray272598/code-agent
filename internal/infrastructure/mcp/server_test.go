package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/jsonrpc"
)

type testTool struct {
	name        string
	description string
	schema      map[string]any
}

func (t *testTool) Name() string                { return t.name }
func (t *testTool) Description() string         { return t.description }
func (t *testTool) InputSchema() map[string]any { return t.schema }
func (t *testTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	return tool.Result{Text: "ok:" + t.name}, nil
}

type testRegistry struct {
	tools map[string]tool.ITool
}

func newTestRegistry() *testRegistry {
	r := &testRegistry{tools: make(map[string]tool.ITool)}
	r.tools["echo"] = &testTool{name: "echo", description: "echo tool", schema: map[string]any{"type": "object"}}
	r.tools["add"] = &testTool{name: "add", description: "add tool", schema: map[string]any{"type": "object"}}
	return r
}

func (r *testRegistry) Get(name string) tool.ITool { return r.tools[name] }
func (r *testRegistry) List() []tool.ITool {
	result := make([]tool.ITool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}
func (r *testRegistry) Count() int { return len(r.tools) }

type testResourceProvider struct{}

func (p *testResourceProvider) ListResources(ctx context.Context) ([]port.Resource, error) {
	return []port.Resource{
		{URI: "file:///test.go", Name: "test.go", MimeType: "text/go"},
	}, nil
}

func (p *testResourceProvider) ReadResource(ctx context.Context, uri string) (*port.ResourceContent, error) {
	return &port.ResourceContent{URI: uri, Text: "package main"}, nil
}

type testPromptProvider struct{}

func (p *testPromptProvider) ListPrompts(ctx context.Context) ([]port.Prompt, error) {
	return []port.Prompt{
		{Name: "review", Description: "code review prompt"},
	}, nil
}

func (p *testPromptProvider) GetPrompt(ctx context.Context, name string, args map[string]string) ([]port.PromptMessage, error) {
	return []port.PromptMessage{
		{Role: "user", Content: "review this code"},
	}, nil
}

func call(t *testing.T, server *jsonrpc.Server, method string, params any) json.RawMessage {
	t.Helper()
	id := jsonrpc.MustID("test-1")
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		paramsRaw = b
	}
	result, err := server.CallHandler(context.Background(), id, method, paramsRaw)
	if err != nil {
		t.Fatalf("call %s failed: %v", method, err)
	}
	b, _ := json.Marshal(result)
	return b
}

func TestMCPServerInitialize(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry)
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	var r initializeResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if r.ProtocolVersion != MCPProtocolVersion {
		t.Errorf("expected %s, got %s", MCPProtocolVersion, r.ProtocolVersion)
	}
	if r.ServerInfo.Name != MCPServerName {
		t.Errorf("expected %s, got %s", MCPServerName, r.ServerInfo.Name)
	}
	if _, ok := r.Capabilities["tools"]; !ok {
		t.Error("expected tools capability")
	}
}

func TestMCPServerToolsList(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry)
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "tools/list", nil)

	var r mcpToolsListResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(r.Tools))
	}
	names := map[string]bool{}
	for _, tool := range r.Tools {
		names[tool.Name] = true
	}
	if !names["echo"] || !names["add"] {
		t.Errorf("expected echo and add, got %v", names)
	}
}

func TestMCPServerToolsCall(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry)
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hello"},
	})

	var r mcpToolsCallResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Error("expected no error")
	}
	if len(r.Content) != 1 || r.Content[0].Text != "ok:echo" {
		t.Errorf("expected ok:echo, got %v", r.Content)
	}
}

func TestMCPServerToolsCallNotFound(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry)
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	err := srv.CallHandlerDirect(context.Background(), jsonrpc.MustID("1"), "tools/call", json.RawMessage(`{"name":"nonexistent"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMCPServerResources(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry).WithResources(&testResourceProvider{})
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "resources/list", nil)

	var r mcpResourcesListResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(r.Resources))
	}
	if r.Resources[0].Name != "test.go" {
		t.Errorf("expected test.go, got %s", r.Resources[0].Name)
	}
}

func TestMCPServerPrompts(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry).WithPrompts(&testPromptProvider{})
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "prompts/list", nil)

	var r mcpPromptsListResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(r.Prompts))
	}
	if r.Prompts[0].Name != "review" {
		t.Errorf("expected review, got %s", r.Prompts[0].Name)
	}
}

func TestMCPServerPromptsGet(t *testing.T) {
	registry := newTestRegistry()
	mcpServer := NewMCPServer(registry).WithPrompts(&testPromptProvider{})
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "prompts/get", map[string]any{
		"name":      "review",
		"arguments": map[string]any{"file": "main.go"},
	})

	var r mcpPromptsGetResult
	if err := json.Unmarshal(result, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(r.Messages))
	}
	if r.Messages[0].Content != "review this code" {
		t.Errorf("expected review this code, got %s", r.Messages[0].Content)
	}
}

func TestMCPServerPing(t *testing.T) {
	mcpServer := NewMCPServer(nil)
	srv := jsonrpc.NewServer()
	mcpServer.RegisterHandlers(srv)

	result := call(t, srv, "ping", nil)
	if string(result) != "{}" {
		t.Errorf("expected {}, got %s", result)
	}
}
