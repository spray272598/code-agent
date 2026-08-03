package service

import (
	"context"
	"log"
	"sync"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolBridge syncs MCP tools into the agent ToolRegistry.
// Depends only on IMCPManagerPort (DIP).
type ToolBridge struct {
	mu       sync.Mutex
	manager  mcpport.IMCPManagerPort
	registry *tool.MapRegistry
	mcpNames map[string]bool
}

func NewToolBridge(manager mcpport.IMCPManagerPort, registry *tool.MapRegistry) *ToolBridge {
	return &ToolBridge{
		manager: manager, registry: registry, mcpNames: map[string]bool{},
	}
}

// Sync replaces MCP tools in registry from manager.ListTools.
func (b *ToolBridge) Sync(ctx context.Context) error {
	if b.manager == nil || b.registry == nil {
		return nil
	}
	defs, err := b.manager.ListTools(ctx)
	if err != nil {
		return err
	}
	b.apply(defs)
	return nil
}

func (b *ToolBridge) ApplyDefs(defs []model.ToolDef) {
	b.apply(defs)
}

func (b *ToolBridge) apply(defs []model.ToolDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for name := range b.mcpNames {
		b.registry.Unregister(name)
	}
	b.mcpNames = map[string]bool{}
	for _, d := range defs {
		b.registry.Register(NewMCPTool(d, b.manager))
		b.mcpNames[d.Name] = true
	}
	log.Printf("[mcp-bridge] synced %d tools\n", len(defs))
}

// MCPTool adapts model.ToolDef to tool.ITool via manager.
type MCPTool struct {
	def model.ToolDef
	mgr mcpport.IMCPManagerPort
}

func NewMCPTool(def model.ToolDef, mgr mcpport.IMCPManagerPort) *MCPTool {
	return &MCPTool{def: def, mgr: mgr}
}

func (t *MCPTool) Name() string { return t.def.Name }
func (t *MCPTool) Description() string {
	if t.def.ServerName != "" {
		return "[MCP:" + t.def.ServerName + "] " + t.def.Description
	}
	return t.def.Description
}
func (t *MCPTool) InputSchema() map[string]any {
	if t.def.InputSchema != nil {
		return t.def.InputSchema
	}
	return map[string]any{"type": "object"}
}
func (t *MCPTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	if t.mgr == nil {
		return tool.Result{Text: "mcp manager unavailable", IsError: true}, nil
	}
	if args == nil {
		args = map[string]any{}
	}
	text, err := t.mgr.CallTool(ctx, t.def.Name, args)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	return tool.Result{Text: text}, nil
}
