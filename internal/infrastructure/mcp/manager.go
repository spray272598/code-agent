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
	mu          sync.RWMutex
	clients     map[string]mcpport.IMCPClient
	configs     map[string]model.ServerConfig
	toolRoute   map[string]string // toolName -> server\x00realName
	toolDefs    []model.ToolDef
	onChange    func([]model.ToolDef)
	lastErr     map[string]string
	stopWatch   chan struct{}
	watchOnce   sync.Once
	reconnectMu sync.Mutex
}

func NewManager() *Manager {
	m := &Manager{
		clients:   make(map[string]mcpport.IMCPClient),
		configs:   make(map[string]model.ServerConfig),
		toolRoute: make(map[string]string),
		lastErr:   make(map[string]string),
		stopWatch: make(chan struct{}),
	}
	m.startWatchdog()
	return m
}

// startWatchdog periodically restarts offline enabled servers.
func (m *Manager) startWatchdog() {
	m.watchOnce.Do(func() {
		go func() {
			t := time.NewTicker(15 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-m.stopWatch:
					return
				case <-t.C:
					m.reconnectOffline()
				}
			}
		}()
	})
}

func (m *Manager) reconnectOffline() {
	m.mu.RLock()
	var need []model.ServerConfig
	for name, cfg := range m.configs {
		if !cfg.Enabled {
			continue
		}
		if _, online := m.clients[name]; !online {
			need = append(need, cfg)
		}
	}
	m.mu.RUnlock()
	for _, cfg := range need {
		log.Printf("[mcp] watchdog reconnect %s\n", cfg.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := m.reconnect(ctx, cfg.Name)
		cancel()
		if err != nil {
			log.Printf("[mcp] reconnect %s failed: %v\n", cfg.Name, err)
		}
	}
}

// reconnect restarts a server by name using stored config.
func (m *Manager) reconnect(ctx context.Context, name string) error {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	m.mu.RLock()
	cfg, ok := m.configs[name]
	m.mu.RUnlock()
	if !ok || !cfg.Enabled {
		return fmt.Errorf("no config for %s", name)
	}
	if err := m.startOne(ctx, cfg); err != nil {
		m.mu.Lock()
		m.lastErr[name] = err.Error()
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	delete(m.lastErr, name)
	m.mu.Unlock()
	_, err := m.refreshTools(ctx)
	return err
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
	serverName, realName := parts[0], parts[1]
	m.mu.RLock()
	client := m.clients[serverName]
	m.mu.RUnlock()
	if client == nil {
		// auto-reconnect once
		if err := m.reconnect(ctx, serverName); err != nil {
			return "", fmt.Errorf("mcp server offline: %s (%v)", serverName, err)
		}
		m.mu.RLock()
		client = m.clients[serverName]
		m.mu.RUnlock()
		if client == nil {
			return "", fmt.Errorf("mcp server offline: %s", serverName)
		}
	}
	text, err := client.CallTool(ctx, realName, args)
	if err != nil {
		// process crash / broken pipe → reconnect and retry once
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "broken pipe") || strings.Contains(low, "eof") ||
			strings.Contains(low, "exit") || strings.Contains(low, "closed") ||
			strings.Contains(low, "process") {
			log.Printf("[mcp] call failed, reconnect %s: %v\n", serverName, err)
			if rerr := m.reconnect(ctx, serverName); rerr == nil {
				m.mu.RLock()
				client = m.clients[serverName]
				m.mu.RUnlock()
				if client != nil {
					return client.CallTool(ctx, realName, args)
				}
			}
		}
		return "", err
	}
	return text, nil
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
	select {
	case <-m.stopWatch:
	default:
		close(m.stopWatch)
	}
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
		pairs = append(pairs, pair{c: c, tools: tools})
	}
	for _, p := range pairs {
		for _, t := range p.tools {
			// Always server__tool: Guard MCP policy + no collision with core tools
			name := p.c.Name() + "__" + t.Name
			route[name] = p.c.Name() + "\x00" + t.Name
			td := t
			td.Name = name
			td.ServerName = p.c.Name()
			td.Description = fmt.Sprintf("[MCP:%s] %s", p.c.Name(), t.Description)
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
