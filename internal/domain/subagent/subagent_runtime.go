package subagent

import (
	"fmt"
	"sync"
	"time"
)

// SubagentRuntime manages the execution context for subagents, including
// tool inheritance, MCP server configuration, and progress tracking.
type SubagentRuntime struct {
	mu sync.RWMutex

	parentCtx     ParentContext
	toolRegistry  map[string]ToolDefinition
	mcpServers    map[string]MCPServerInfo
	progressMap   map[string]*runtimeProgress
	progressCh    chan SubagentProgress
	maxLiveTokens int64
	totalTokens   int64
}

type runtimeProgress struct {
	taskID     string
	tokensUsed int64
	step       int
	message    string
	lastUpdate time.Time
}

// NewSubagentRuntime creates a new subagent runtime.
func NewSubagentRuntime(parentCtx ParentContext) *SubagentRuntime {
	r := &SubagentRuntime{
		parentCtx:     parentCtx,
		toolRegistry:  make(map[string]ToolDefinition),
		mcpServers:    make(map[string]MCPServerInfo),
		progressMap:   make(map[string]*runtimeProgress),
		progressCh:    make(chan SubagentProgress, 128),
		maxLiveTokens: 50_000,
	}

	// Inherit parent tool definitions
	for _, td := range parentCtx.ToolDefinitions {
		r.toolRegistry[td.Name] = td
	}
	// Inherit parent MCP servers
	for _, mcp := range parentCtx.MCPServers {
		r.mcpServers[mcp.Name] = mcp
	}
	return r
}

// InheritTool adds a tool definition to the runtime.
func (r *SubagentRuntime) InheritTool(td ToolDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolRegistry[td.Name] = td
}

// InheritMCPServer adds an MCP server to the runtime.
func (r *SubagentRuntime) InheritMCPServer(mcp MCPServerInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mcpServers[mcp.Name] = mcp
}

// AvailableTools returns all tool definitions available to subagents.
func (r *SubagentRuntime) AvailableTools() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]ToolDefinition, 0, len(r.toolRegistry))
	for _, td := range r.toolRegistry {
		tools = append(tools, td)
	}
	return tools
}

// AvailableMCPServers returns all MCP servers available to subagents.
func (r *SubagentRuntime) AvailableMCPServers() []MCPServerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	servers := make([]MCPServerInfo, 0, len(r.mcpServers))
	for _, mcp := range r.mcpServers {
		servers = append(servers, mcp)
	}
	return servers
}

// StartProgress initializes progress tracking for a new task.
func (r *SubagentRuntime) StartProgress(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progressMap[taskID] = &runtimeProgress{
		taskID:     taskID,
		lastUpdate: time.Now(),
	}
}

// UpdateProgress updates the progress of a running task.
func (r *SubagentRuntime) UpdateProgress(taskID string, tokens int64, step int, message string) {
	r.mu.RLock()
	prog, ok := r.progressMap[taskID]
	r.mu.RUnlock()

	if ok {
		r.mu.Lock()
		prog.tokensUsed = tokens
		prog.step = step
		prog.message = message
		prog.lastUpdate = time.Now()
		r.totalTokens += tokens
		r.mu.Unlock()
	}

	select {
	case r.progressCh <- SubagentProgress{
		TaskID:         taskID,
		TokensUsed:     tokens,
		LiveTokens:     tokens,
		FinishedTokens: r.totalTokens,
		Step:           step,
		Message:        message,
		Timestamp:      time.Now(),
	}:
	default:
	}
}

// EndProgress finalizes progress tracking for a completed task.
func (r *SubagentRuntime) EndProgress(taskID string) {
	r.mu.Lock()
	delete(r.progressMap, taskID)
	r.mu.Unlock()
}

// ProgressChan returns the channel for progress updates.
func (r *SubagentRuntime) ProgressChan() <-chan SubagentProgress {
	return r.progressCh
}

// TotalTokens returns the total tokens used by all subagents.
func (r *SubagentRuntime) TotalTokens() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.totalTokens
}

// ParentSessionID returns the parent session ID.
func (r *SubagentRuntime) ParentSessionID() string {
	return r.parentCtx.SessionID
}

// BuildPromptContext constructs the effective prompt context for a subagent.
func (r *SubagentRuntime) BuildPromptContext(input SubagentInput) string {
	var ctx string
	if input.Description != "" {
		ctx += fmt.Sprintf("## Task\n%s\n\n", input.Description)
	}
	ctx += "## Tools Available\n"
	for _, td := range r.AvailableTools() {
		ctx += fmt.Sprintf("- **%s**: %s\n", td.Name, td.Description)
	}
	if len(r.AvailableMCPServers()) > 0 {
		ctx += "\n## MCP Servers Connected\n"
		for _, mcp := range r.AvailableMCPServers() {
			ctx += fmt.Sprintf("- %s: %s (tools: %d)\n", mcp.Name, mcp.Endpoint, len(mcp.Tools))
		}
	}
	if input.ParentSession != "" {
		ctx += fmt.Sprintf("\n## Parent Session\nInherited from: %s\n", input.ParentSession)
	}
	return ctx
}

// SubagentTypeResolver validates and resolves subagent types.
type SubagentTypeResolver struct {
	mu    sync.RWMutex
	types map[SubagentType]SubagentTypeInfo
}

// NewSubagentTypeResolver creates a resolver with built-in types.
func NewSubagentTypeResolver() *SubagentTypeResolver {
	r := &SubagentTypeResolver{
		types: make(map[SubagentType]SubagentTypeInfo),
	}
	for _, info := range BuiltinSubagentTypes() {
		r.types[info.Type] = info
	}
	return r
}

// Register adds a custom subagent type.
func (r *SubagentTypeResolver) Register(info SubagentTypeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[info.Type] = info
}

// Validate checks if a subagent type is registered and valid.
func (r *SubagentTypeResolver) Validate(st SubagentType) (SubagentTypeInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.types[st]
	if !ok {
		return SubagentTypeInfo{}, fmt.Errorf("unknown subagent type: %s", st)
	}
	if len(info.Tools) == 0 {
		return SubagentTypeInfo{}, fmt.Errorf("subagent type %s has no tools", st)
	}
	return info, nil
}

// Describe returns the description of a subagent type.
func (r *SubagentTypeResolver) Describe(st SubagentType) (SubagentTypeInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.types[st]
	return info, ok
}

// List returns all registered subagent types.
func (r *SubagentTypeResolver) List() []SubagentTypeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]SubagentTypeInfo, 0, len(r.types))
	for _, info := range r.types {
		infos = append(infos, info)
	}
	return infos
}

// ValidateAndResolve validates input and resolves the subagent type.
func (r *SubagentTypeResolver) ValidateAndResolve(input *SubagentInput) (SubagentTypeInfo, error) {
	if err := input.Validate(); err != nil {
		return SubagentTypeInfo{}, err
	}
	if input.SubagentType == "" {
		input.SubagentType = SubagentTypeGeneralPurpose
	}
	return r.Validate(input.SubagentType)
}
