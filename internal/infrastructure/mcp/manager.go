package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// Manager implements mcpport.IMCPManagerPort. Lives in infrastructure only.
type Manager struct {
	mu        sync.RWMutex
	clients   map[string]mcpport.IMCPClient
	configs   map[string]model.ServerConfig
	toolRoute map[string]string // toolName -> server\x00realName
	toolDefs  []model.ToolDef
	onChange  func([]model.ToolDef)
	lastErr   map[string]string
}

func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]mcpport.IMCPClient),
		configs: make(map[string]model.ServerConfig),
		toolRoute: make(map[string]string),
		lastErr: make(map[string]string),
	}
}

func (m *Manager) OnToolsChanged(cb func([]model.ToolDef)) {
	m.mu.Lock()
	m.onChange = cb
	m.mu.Unlock()
}

func (m *Manager) AddOrUpdate(ctx context.Context, cfg model.ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("name required")
	}
	if cfg.Transport == "" {
		cfg.Transport = "stdio"
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 60
	}
	m.mu.Lock()
	m.configs[cfg.Name] = cfg
	m.mu.Unlock()
	if !cfg.Enabled {
		return m.Remove(cfg.Name)
	}
	if err := m.startOne(ctx, cfg); err != nil {
		m.mu.Lock()
		m.lastErr[cfg.Name] = err.Error()
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	delete(m.lastErr, cfg.Name)
	m.mu.Unlock()
	_, err := m.refreshTools(ctx)
	return err
}

func (m *Manager) startOne(ctx context.Context, cfg model.ServerConfig) error {
	m.mu.Lock()
	if old, ok := m.clients[cfg.Name]; ok {
		_ = old.Close()
		delete(m.clients, cfg.Name)
	}
	m.mu.Unlock()

	var client mcpport.IMCPClient
	switch strings.ToLower(cfg.Transport) {
	case "stdio", "":
		client = NewStdioClient(cfg)
	default:
		return fmt.Errorf("transport %s not implemented yet (use stdio)", cfg.Transport)
	}
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return err
	}
	m.mu.Lock()
	m.clients[cfg.Name] = client
	m.configs[cfg.Name] = cfg
	m.mu.Unlock()
	return nil
}

func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	if c, ok := m.clients[name]; ok {
		_ = c.Close()
		delete(m.clients, name)
	}
	delete(m.configs, name)
	delete(m.lastErr, name)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.refreshTools(ctx)
	return err
}

func (m *Manager) ListTools(ctx context.Context) ([]model.ToolDef, error) {
	m.mu.RLock()
	if len(m.toolDefs) > 0 {
		out := append([]model.ToolDef{}, m.toolDefs...)
		m.mu.RUnlock()
		return out, nil
	}
	m.mu.RUnlock()
	return m.refreshTools(ctx)
}

func (m *Manager) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	m.mu.RLock()
	route, ok := m.toolRoute[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("mcp tool not found: %s", name)
	}
	parts := strings.SplitN(route, "\x00", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid route")
	}
	m.mu.RLock()
	client := m.clients[parts[0]]
	m.mu.RUnlock()
	if client == nil {
		return "", fmt.Errorf("mcp server offline: %s", parts[0])
	}
	return client.CallTool(ctx, parts[1], args)
}

func (m *Manager) IsOnline(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.clients[name]
	return ok
}

func (m *Manager) Health(ctx context.Context) []model.HealthStatus {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	// count tools per server
	counts := map[string]int{}
	for _, t := range m.toolDefs {
		counts[t.ServerName]++
	}
	var out []model.HealthStatus
	for name, cfg := range m.configs {
		_, online := m.clients[name]
		out = append(out, model.HealthStatus{
			Name: name, Online: online, Transport: cfg.Transport,
			ToolCount: counts[name], LastError: m.lastErr[name], Enabled: cfg.Enabled,
		})
	}
	return out
}

func (m *Manager) ListServers() []model.ServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.ServerConfig, 0, len(m.configs))
	for _, c := range m.configs {
		out = append(out, c)
	}
	return out
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for n, c := range m.clients {
		_ = c.Close()
		delete(m.clients, n)
	}
	return nil
}

func (m *Manager) refreshTools(ctx context.Context) ([]model.ToolDef, error) {
	m.mu.RLock()
	clients := make([]mcpport.IMCPClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	cb := m.onChange
	m.mu.RUnlock()

	route := map[string]string{}
	var defs []model.ToolDef
	nameCount := map[string]int{}
	type pair struct {
		c     mcpport.IMCPClient
		tools []model.ToolDef
	}
	var pairs []pair
	for _, c := range clients {
		tools, err := c.ListTools(ctx)
		if err != nil {
			log.Printf("[mcp] list tools %s: %v\n", c.Name(), err)
			continue
		}
		for _, t := range tools {
			nameCount[t.Name]++
		}
		pairs = append(pairs, pair{c: c, tools: tools})
	}
	for _, p := range pairs {
		for _, t := range p.tools {
			name := t.Name
			if nameCount[t.Name] > 1 {
				name = p.c.Name() + "__" + t.Name
			}
			route[name] = p.c.Name() + "\x00" + t.Name
			td := t
			td.Name = name
			td.ServerName = p.c.Name()
			if name != t.Name {
				td.Description = fmt.Sprintf("[%s] %s", p.c.Name(), t.Description)
			}
			defs = append(defs, td)
		}
	}
	m.mu.Lock()
	m.toolRoute = route
	m.toolDefs = defs
	m.mu.Unlock()
	if cb != nil {
		cb(defs)
	}
	return defs, nil
}

// Ensure Manager implements the port.
var _ mcpport.IMCPManagerPort = (*Manager)(nil)
