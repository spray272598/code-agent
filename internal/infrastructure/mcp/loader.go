package mcp

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// mcpJSON is the VS Code / Claude Desktop style MCP config file shape:
// { "mcpServers": { "<name>": { ... } } }.
type mcpJSON struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string            `json:"type"`    // "stdio" | "sse" | "http" (optional; defaults to stdio)
	Command string            `json:"command"` // required for stdio
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"` // required for http/sse
	Headers map[string]string `json:"headers"`
	Enabled *bool             `json:"enabled"` // nil → default true
	Timeout int               `json:"timeout"` // seconds; 0 → default 60
}

// LoadServersFromFile reads an mcp.json file and converts each entry into a
// domain ServerConfig. Errors on unreadable/malformed files; unknown transport
// types are skipped with a descriptive error instead of failing the whole load.
func LoadServersFromFile(path string) ([]model.ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp config %s: %w", path, err)
	}
	var raw mcpJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	servers := make([]model.ServerConfig, 0, len(raw.MCPServers))
	for name, e := range raw.MCPServers {
		cfg, err := toServerConfig(name, e)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", name, err)
		}
		servers = append(servers, cfg)
	}
	return servers, nil
}

func toServerConfig(name string, e mcpServerEntry) (model.ServerConfig, error) {
	transport := e.Type
	if transport == "" {
		transport = "stdio"
	}
	cfg := model.ServerConfig{
		Name:       name,
		Transport:  transport,
		Command:    e.Command,
		Args:       e.Args,
		URL:        e.URL,
		Enabled:    true,
		TimeoutSec: e.Timeout,
	}
	if e.Enabled != nil {
		cfg.Enabled = *e.Enabled
	}
	// Env carries both process env (stdio) and extra headers (http).
	env := map[string]string{}
	for k, v := range e.Env {
		env[k] = v
	}
	for k, v := range e.Headers {
		env[k] = v
	}
	if len(env) > 0 {
		cfg.Env = env
	}
	switch transport {
	case "stdio":
		if cfg.Command == "" {
			return cfg, fmt.Errorf("stdio transport requires \"command\"")
		}
	case "sse", "http", "streamable", "streamable-http":
		if cfg.URL == "" {
			return cfg, fmt.Errorf("%s transport requires \"url\"", transport)
		}
	default:
		return cfg, fmt.Errorf("unsupported transport %q (use stdio, sse, or http)", transport)
	}
	return cfg, nil
}
