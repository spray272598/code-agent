package http

// MCP (Model Context Protocol) handlers: server management (install/remove),
// health check, and tool listing. Per-user factory resolves the Manager for
// the authenticated tenant; cross-tenant reads return ErrTenantMismatch.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	// authJWT already ran; principal is on ctx. Use the per-user factory so
	// user A never sees user B's server list.
	m, err := s.app.MCPFor(r.Context())
	if err != nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"code": "0000", "data": m.Health(r.Context())})
	case http.MethodPost:
		var body struct {
			Name       string            `json:"name"`
			Transport  string            `json:"transport"`
			Command    string            `json:"command"`
			Args       []string          `json:"args"`
			Env        map[string]string `json:"env"`
			URL        string            `json:"url"`
			Enabled    *bool             `json:"enabled"`
			TimeoutSec int               `json:"timeoutSec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		en := true
		if body.Enabled != nil {
			en = *body.Enabled
		}
		cfg := struct {
			// use model via map rebuild
		}{}
		_ = cfg
		// import model in handler - use mcp through app method Install
		type serverCfg = struct {
			Name, Transport, Command, URL string
			Args                          []string
			Env                           map[string]string
			Enabled                       bool
			TimeoutSec                    int
		}
		// call via interface extension — install on manager through ChatApp helper
		err := s.installMCP(r.Context(), body.Name, body.Transport, body.Command, body.Args, body.Env, body.URL, en, body.TimeoutSec)
		if err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		tools, _ := m.ListTools(r.Context())
		names := make([]string, 0)
		for _, t := range tools {
			if t.ServerName == body.Name || strings.HasPrefix(t.Name, body.Name+"__") {
				names = append(names, t.Name)
			}
		}
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
			"ok": true, "name": body.Name, "toolCount": len(names), "tools": names,
		}})
	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			name = body.Name
		}
		if err := m.Remove(name); err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]bool{"deleted": true}})
	default:
		writeJSON(w, 405, map[string]any{"code": "405"})
	}
}

func (s *Server) installMCP(ctx context.Context, name, transport, command string, args []string, env map[string]string, url string, enabled bool, timeout int) error {
	// use type from domain model via bootstrap-installed factory — dynamic import
	return s.app.InstallMCP(ctx, name, transport, command, args, env, url, enabled, timeout)
}

func (s *Server) handleMCPHealth(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.MCPFor(r.Context())
	if err != nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": m.Health(r.Context())})
}

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.MCPFor(r.Context())
	if err != nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": err.Error()})
		return
	}
	list, err := m.ListTools(r.Context())
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	server := r.URL.Query().Get("server")
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		if server != "" && t.ServerName != server {
			continue
		}
		out = append(out, map[string]any{"name": t.Name, "description": t.Description, "server": t.ServerName})
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": out})
}
