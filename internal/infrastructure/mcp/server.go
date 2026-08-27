package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/jsonrpc"
)

const (
	MCPProtocolVersion = "2024-11-05"
	MCPServerName      = "code-agent"
	MCPServerVersion   = "1.0.0"
)

type ToolReader interface {
	Get(name string) tool.ITool
	List() []tool.ITool
	Count() int
}

type MCPServer struct {
	registry  ToolReader
	resources port.ResourceProvider
	prompts   port.PromptProvider
	sessions  map[string]*mcpSession
	mu        sync.RWMutex
}

type mcpSession struct {
	protocolVersion string
	capabilities    map[string]any
	clientInfo      mcpClientInfo
}

type mcpClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func NewMCPServer(registry ToolReader) *MCPServer {
	return &MCPServer{
		registry: registry,
		sessions: make(map[string]*mcpSession),
	}
}

func (s *MCPServer) WithResources(r port.ResourceProvider) *MCPServer {
	s.resources = r
	return s
}

func (s *MCPServer) WithPrompts(p port.PromptProvider) *MCPServer {
	s.prompts = p
	return s
}

func (s *MCPServer) RegisterHandlers(server *jsonrpc.Server) {
	server.Handle("initialize", s.handleInitialize)
	server.Handle("ping", s.handlePing)
	server.Handle("tools/list", s.handleToolsList)
	server.Handle("tools/call", s.handleToolsCall)
	server.Handle("resources/list", s.handleResourcesList)
	server.Handle("resources/read", s.handleResourcesRead)
	server.Handle("prompts/list", s.handlePromptsList)
	server.Handle("prompts/get", s.handlePromptsGet)
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *MCPServer) handleInitialize(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	var p struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ClientInfo      mcpClientInfo  `json:"clientInfo"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInvalidParams, "invalid params: "+err.Error())
	}

	sess := &mcpSession{
		protocolVersion: p.ProtocolVersion,
		capabilities:    p.Capabilities,
		clientInfo:      p.ClientInfo,
	}
	key := string(id)
	s.mu.Lock()
	s.sessions[key] = sess
	s.mu.Unlock()

	log.Printf("[mcp] initialize: client=%s/%s protocol=%s", p.ClientInfo.Name, p.ClientInfo.Version, p.ProtocolVersion)

	caps := map[string]any{}
	if s.registry != nil && s.registry.Count() > 0 {
		caps["tools"] = map[string]any{"listChanged": false}
	}
	if s.resources != nil {
		caps["resources"] = map[string]any{"subscribe": false, "listChanged": false}
	}
	if s.prompts != nil {
		caps["prompts"] = map[string]any{"listChanged": false}
	}

	return initializeResult{
		ProtocolVersion: MCPProtocolVersion,
		Capabilities:    caps,
		ServerInfo: serverInfo{
			Name:    MCPServerName,
			Version: MCPServerVersion,
		},
	}, nil
}

func (s *MCPServer) handlePing(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	return struct{}{}, nil
}

type mcpToolsListResult struct {
	Tools []mcpServerToolDef `json:"tools"`
}

type mcpServerToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *MCPServer) handleToolsList(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	if s.registry == nil {
		return mcpToolsListResult{Tools: []mcpServerToolDef{}}, nil
	}

	tools := s.registry.List()
	result := make([]mcpServerToolDef, 0, len(tools))
	for _, t := range tools {
		result = append(result, mcpServerToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return mcpToolsListResult{Tools: result}, nil
}

type mcpToolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type mcpToolsCallResult struct {
	Content []mcpContentItem `json:"content"`
	IsError bool             `json:"isError"`
}

type mcpContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *MCPServer) handleToolsCall(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	var p mcpToolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInvalidParams, "invalid params: "+err.Error())
	}

	if s.registry == nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeToolNotFound, "tool not found: "+p.Name)
	}

	t := s.registry.Get(p.Name)
	if t == nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeToolNotFound, "tool not found: "+p.Name)
	}

	result, err := t.Execute(ctx, p.Arguments)
	if err != nil {
		return mcpToolsCallResult{
			Content: []mcpContentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}

	return mcpToolsCallResult{
		Content: []mcpContentItem{{Type: "text", Text: result.Text}},
		IsError: result.IsError,
	}, nil
}

type mcpResourcesListResult struct {
	Resources []port.Resource `json:"resources"`
}

func (s *MCPServer) handleResourcesList(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	if s.resources == nil {
		return mcpResourcesListResult{Resources: []port.Resource{}}, nil
	}
	resources, err := s.resources.ListResources(ctx)
	if err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInternalError, err.Error())
	}
	return mcpResourcesListResult{Resources: resources}, nil
}

type mcpResourcesReadParams struct {
	URI string `json:"uri"`
}

func (s *MCPServer) handleResourcesRead(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	if s.resources == nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeMethodNotFound, "resources not supported")
	}
	var p mcpResourcesReadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInvalidParams, "invalid params: "+err.Error())
	}
	content, err := s.resources.ReadResource(ctx, p.URI)
	if err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInternalError, err.Error())
	}
	return content, nil
}

type mcpPromptsListResult struct {
	Prompts []port.Prompt `json:"prompts"`
}

func (s *MCPServer) handlePromptsList(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	if s.prompts == nil {
		return mcpPromptsListResult{Prompts: []port.Prompt{}}, nil
	}
	prompts, err := s.prompts.ListPrompts(ctx)
	if err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInternalError, err.Error())
	}
	return mcpPromptsListResult{Prompts: prompts}, nil
}

type mcpPromptsGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type mcpPromptsGetResult struct {
	Messages []port.PromptMessage `json:"messages"`
}

func (s *MCPServer) handlePromptsGet(ctx context.Context, id jsonrpc.ID, method string, params json.RawMessage) (any, error) {
	if s.prompts == nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeMethodNotFound, "prompts not supported")
	}
	var p mcpPromptsGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, jsonrpc.NewError(jsonrpc.CodeInvalidParams, "invalid params: "+err.Error())
	}
	messages, err := s.prompts.GetPrompt(ctx, p.Name, p.Arguments)
	if err != nil {
		return nil, fmt.Errorf("prompt %q: %w", p.Name, err)
	}
	return mcpPromptsGetResult{Messages: messages}, nil
}
