package service

import (
	"context"
	"errors"
	"log"
	"sync"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	mcpcache "github.com/spray272598/code-agent/internal/domain/mcp/cache"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolBridge syncs MCP tools into the agent ToolRegistry. Sprint 1.6: the
// bridge still holds the system-level manager for bootstrap-loaded servers,
// but MCPTool.Execute routes calls through the per-user factory so the
// authenticated tenant always reaches its own tool space.
type ToolBridge struct {
	mu       sync.Mutex
	manager  mcpport.IMCPManagerPort
	factory  mcpport.IMCPManagerFactory
	registry *tool.MapRegistry
	mcpNames map[string]bool
	cache    *mcpcache.ToolCache
}

func NewToolBridge(manager mcpport.IMCPManagerPort, registry *tool.MapRegistry) *ToolBridge {
	return &ToolBridge{
		manager: manager, registry: registry, mcpNames: map[string]bool{},
	}
}

// NewToolBridgeWithFactory stores the per-user factory on the bridge so
// MCPTool.Execute can resolve the right Manager for the authenticated tenant.
func NewToolBridgeWithFactory(f mcpport.IMCPManagerFactory, registry *tool.MapRegistry) *ToolBridge {
	return &ToolBridge{factory: f, registry: registry, mcpNames: map[string]bool{}}
}

// WithCache attaches a ToolCache to the bridge. Cacheable(def) is evaluated
// at apply-time so the bridge only caches safe read-only tools.
func (b *ToolBridge) WithCache(c *mcpcache.ToolCache) *ToolBridge {
	b.cache = c
	return b
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
		var mcpTool IToolResolver = directResolver{mgr: b.manager}
		if b.factory != nil {
			mcpTool = factoryResolver{f: b.factory}
		}
		cacheable := mcpcache.Cacheable(d)
		tool := NewMCPTool(d, mcpTool, b.cache, cacheable)
		b.registry.Register(tool)
		b.mcpNames[d.Name] = true
	}
	if b.cache != nil {
		b.cache.Invalidate("")
	}
	log.Printf("[mcp-bridge] synced %d tools (cache=%v)\n", len(defs), b.cache != nil)
}

// IToolResolver abstracts where MCPTool.Execute obtains a Manager. The
// concrete impl is either a direct system Manager or a per-user factory.
type IToolResolver interface {
	Resolve(ctx context.Context) (mcpport.IMCPManagerPort, error)
}

type (
	directResolver  struct{ mgr mcpport.IMCPManagerPort }
	factoryResolver struct {
		f mcpport.IMCPManagerFactory
	}
)

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
	def       model.ToolDef
	resolve   IToolResolver
	cache     *mcpcache.ToolCache
	cacheable bool
}

func NewMCPTool(def model.ToolDef, resolve IToolResolver, cache *mcpcache.ToolCache, cacheable bool) *MCPTool {
	return &MCPTool{def: def, resolve: resolve, cache: cache, cacheable: cacheable}
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
	if t.def.InputSchema != nil {
		if err := tool.ValidateArgs(t.def.InputSchema, args); err != nil {
			return tool.Result{Text: "mcp validation: " + err.Error(), IsError: true}, nil
		}
	}
	serverName := t.def.ServerName

	// Cache lookup for cacheable tools.
	if t.cacheable && t.cache != nil {
		if hit, ok := t.cache.Get(serverName, t.def.Name, args); ok {
			telemetry.IncMCPCacheHit()
			return tool.Result{Text: hit}, nil
		}
	}

	mgr, err := t.resolve.Resolve(ctx)
	if err != nil || mgr == nil {
		return tool.Result{Text: "mcp manager unavailable: " + errString(err), IsError: true}, nil
	}
	text, err := mgr.CallTool(ctx, t.def.Name, args)
	if err != nil {
		telemetry.IncMCPToolError()
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	// Cache write for cacheable tools on success.
	if t.cacheable && t.cache != nil {
		t.cache.Put(serverName, t.def.Name, args, text)
		telemetry.IncMCPCacheMiss()
	}
	telemetry.IncMCPToolSuccess()
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
