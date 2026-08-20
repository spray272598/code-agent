package service

import (
	"context"
	"errors"
	"log"
	"sync"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolBridge syncs MCP tools into the agent ToolRegistry. Sprint 1.6: the
// bridge still holds the system-level manager for bootstrap-loaded servers,
// but MCPTool.Execute routes calls through the per-user factory so the
// authenticated tenant always reaches its own tool space.
type ToolBridge struct {
	mu       sync.Mutex
	manager  mcpport.IMCPManagerPort
	factory  mcpport.IUserMCPManagerFactory
	registry *tool.MapRegistry
	mcpNames map[string]bool
}

func NewToolBridge(manager mcpport.IMCPManagerPort, registry *tool.MapRegistry) *ToolBridge {
	return &ToolBridge{
		manager: manager, registry: registry, mcpNames: map[string]bool{},
	}
}

// NewToolBridgeWithFactory stores the per-user factory on the bridge so
// MCPTool.Execute can resolve the right Manager for the authenticated tenant.
func NewToolBridgeWithFactory(f mcpport.IUserMCPManagerFactory, registry *tool.MapRegistry) *ToolBridge {
	return &ToolBridge{factory: f, registry: registry, mcpNames: map[string]bool{}}
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
		// Prefer factory resolution (per-user) when available; fall back to the
		// system manager for tools the bootstrap registered before the factory
		// existed (e.g. test setups that bypass bootstrap).
		var mcpTool IToolResolver = directResolver{mgr: b.manager}
		if b.factory != nil {
			mcpTool = factoryResolver{f: b.factory}
		}
		b.registry.Register(NewMCPTool(d, mcpTool))
		b.mcpNames[d.Name] = true
	}
	log.Printf("[mcp-bridge] synced %d tools\n", len(defs))
}

// IToolResolver abstracts where MCPTool.Execute obtains a Manager. The
// concrete impl is either a direct system Manager or a per-user factory.
type IToolResolver interface {
	Resolve(ctx context.Context) (mcpport.IMCPManagerPort, error)
}

type directResolver struct{ mgr mcpport.IMCPManagerPort }
type factoryResolver struct{ f mcpport.IUserMCPManagerFactory }

func (d directResolver) Resolve(_ context.Context) (mcpport.IMCPManagerPort, error) {
	if d.mgr == nil {
		return nil, errors.New("mcp manager unavailable")
	}
	return d.mgr, nil
}
func (f factoryResolver) Resolve(ctx context.Context) (mcpport.IMCPManagerPort, error) {
	return f.f.For(ctx)
}

// MCPTool adapts model.ToolDef to tool.ITool via the resolver.
type MCPTool struct {
	def     model.ToolDef
	resolve IToolResolver
}

func NewMCPTool(def model.ToolDef, resolve IToolResolver) *MCPTool {
	return &MCPTool{def: def, resolve: resolve}
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
	if t.resolve == nil {
		return tool.Result{Text: "mcp manager unavailable", IsError: true}, nil
	}
	if args == nil {
		args = map[string]any{}
	}
	// Schema pre-check when available (security: fail closed on bad shape)
	if t.def.InputSchema != nil {
		if err := tool.ValidateArgs(t.def.InputSchema, args); err != nil {
			return tool.Result{Text: "mcp validation: " + err.Error(), IsError: true}, nil
		}
	}
	mgr, err := t.resolve.Resolve(ctx)
	if err != nil || mgr == nil {
		return tool.Result{Text: "mcp manager unavailable: " + errString(err), IsError: true}, nil
	}
	// Manager routes by registered name (server__tool)
	text, err := mgr.CallTool(ctx, t.def.Name, args)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	return tool.Result{Text: text}, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errors.New("")) {
		return err.Error()
	}
	return err.Error()
}
