package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/application"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/observability"
)

type Server struct {
	app  *application.ChatApp
	addr string
	srv  *http.Server
}

func New(app *application.ChatApp, addr string) *Server {
	return &Server{app: app, addr: addr}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/v1/session", s.handleSession)
	mux.HandleFunc("/api/v1/session/list", s.handleSessionList)
	mux.HandleFunc("/api/v1/chat", s.handleChat)
	mux.HandleFunc("/api/v1/chat/stream", s.handleChatStream)
	mux.HandleFunc("/api/v1/tools", s.handleTools)
	mux.HandleFunc("/api/v1/permission/pending", s.handlePermPending)
	mux.HandleFunc("/api/v1/permission/approve", s.handlePermApprove)
	mux.HandleFunc("/api/v1/permission/reject", s.handlePermReject)
	mux.HandleFunc("/api/v1/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("/api/v1/mcp/health", s.handleMCPHealth)
	mux.HandleFunc("/api/v1/mcp/tools", s.handleMCPTools)
	mux.HandleFunc("/api/v1/skills", s.handleSkills)
	mux.HandleFunc("/api/v1/skills/install", s.handleSkillInstall)
	mux.HandleFunc("/api/v1/skills/uninstall", s.handleSkillUninstall)
	mux.HandleFunc("/api/v1/skills/reload", s.handleSkillReload)
	mux.HandleFunc("/api/v1/memory", s.handleMemory)
	mux.HandleFunc("/api/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/audit", s.handleAudit)

	handler := cors(auth(s.app, observability.AccessLog(observability.RequestIDMiddleware(mux))))
	s.srv = &http.Server{Addr: s.addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("[http] listening on %s\n", s.addr)
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func auth(app *application.ChatApp, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				key = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if !app.Auth(key) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "401", "message": "invalid api key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			UserID    string `json:"userId"`
			ProjectID string `json:"projectId"`
			Title     string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sess, err := s.app.CreateSession(body.UserID, body.ProjectID, body.Title)
		if err != nil {
			writeJSON(w, 500, errMap(err))
			return
		}
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
			"sessionId": sess.ID, "userId": sess.UserID, "projectId": sess.ProjectID, "title": sess.Title,
		}})
	case http.MethodGet:
		id := r.URL.Query().Get("sessionId")
		sess, err := s.app.GetSession(id)
		if err != nil || sess == nil {
			writeJSON(w, 404, map[string]any{"code": "404", "message": "not found"})
			return
		}
		writeJSON(w, 200, map[string]any{"code": "0000", "data": sess})
	default:
		writeJSON(w, 405, map[string]any{"code": "405", "message": "method"})
	}
}

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	list, err := s.app.ListSessions(userID)
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"code": "0000", "data": s.app.ListTools()})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	var req application.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	res, err := s.app.Chat(req)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": res})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	var req application.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	ch, sess, err := s.app.ChatStream(req)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"message": "stream unsupported"})
		return
	}
	fmt.Fprintf(w, "event: session\ndata: {\"sessionId\":%q}\n\n", sess.ID)
	flusher.Flush()
	for ev := range ch {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, string(b))
		flusher.Flush()
	}
}

func (s *Server) handlePermPending(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("sessionId")
	g := s.app.Permission()
	if g == nil {
		writeJSON(w, 200, map[string]any{"code": "0000", "data": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": g.ListPending(sid)})
}

func (s *Server) handlePermApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string `json:"id"`
		Scope     string `json:"scope"`
		Continue  bool   `json:"continue"`
		SessionID string `json:"sessionId"`
		UserID    string `json:"userId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ID == "" {
		writeJSON(w, 400, map[string]any{"message": "id required"})
		return
	}
	if body.Scope == "" {
		body.Scope = "once"
	}
	p, err := s.app.Permission().Approve(body.ID, body.Scope)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	out := map[string]any{"approved": true, "pending": p}
	if body.Continue {
		sid := body.SessionID
		if sid == "" && p != nil {
			sid = p.SessionID
		}
		res, err := s.app.Chat(application.ChatRequest{SessionID: sid, UserID: body.UserID, Message: "继续"})
		if err != nil {
			out["continueError"] = err.Error()
		} else {
			out["chat"] = res
		}
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": out})
}

func (s *Server) handlePermReject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.app.Permission().Reject(body.ID); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]bool{"rejected": true}})
}

func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	m := s.app.MCP()
	if m == nil {
		writeJSON(w, 200, map[string]any{"code": "0000", "data": []any{}})
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
	m := s.app.MCP()
	if m == nil {
		return fmt.Errorf("mcp disabled")
	}
	// use type from domain model via bootstrap-installed manager — dynamic import
	return s.app.InstallMCP(ctx, name, transport, command, args, env, url, enabled, timeout)
}

func (s *Server) handleMCPHealth(w http.ResponseWriter, r *http.Request) {
	m := s.app.MCP()
	if m == nil {
		writeJSON(w, 200, map[string]any{"code": "0000", "data": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": m.Health(r.Context())})
}

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	m := s.app.MCP()
	if m == nil {
		writeJSON(w, 200, map[string]any{"code": "0000", "data": []any{}})
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

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	sk := s.app.Skills()
	if sk == nil {
		writeJSON(w, 200, map[string]any{"code": "0000", "data": []any{}})
		return
	}
	if id := r.URL.Query().Get("id"); id != "" {
		one := sk.Get(id)
		if one == nil {
			writeJSON(w, 404, map[string]any{"code": "404", "message": "not found"})
			return
		}
		writeJSON(w, 200, map[string]any{"code": "0000", "data": one})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": sk.List()})
}

func (s *Server) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	sk := s.app.Skills()
	if sk == nil {
		writeJSON(w, 400, map[string]any{"message": "skills disabled"})
		return
	}
	var body struct {
		Path string `json:"path"`
		ID   string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeJSON(w, 400, map[string]any{"message": "path required"})
		return
	}
	installed, err := sk.InstallFromPath(body.Path, body.ID)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": installed})
}

func (s *Server) handleSkillUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	sk := s.app.Skills()
	if sk == nil {
		writeJSON(w, 400, map[string]any{"message": "skills disabled"})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ID == "" {
		writeJSON(w, 400, map[string]any{"message": "id required"})
		return
	}
	if err := sk.Uninstall(body.ID); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]bool{"uninstalled": true}})
}

func (s *Server) handleSkillReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	sk := s.app.Skills()
	if sk == nil {
		writeJSON(w, 400, map[string]any{"message": "skills disabled"})
		return
	}
	if err := sk.Reload(); err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"reloaded": true, "count": len(sk.List())}})
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		userID := r.URL.Query().Get("userId")
		projectID := r.URL.Query().Get("projectId")
		scope := r.URL.Query().Get("scope")
		q := r.URL.Query().Get("q")
		if q != "" {
			list, err := s.app.SearchMemory(r.Context(), userID, projectID, q, 20)
			if err != nil {
				writeJSON(w, 500, errMap(err))
				return
			}
			writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
			return
		}
		list, err := s.app.ListMemory(r.Context(), userID, projectID, scope, 50)
		if err != nil {
			writeJSON(w, 500, errMap(err))
			return
		}
		writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
	case http.MethodPost:
		var body struct {
			UserID     string `json:"userId"`
			ProjectID  string `json:"projectId"`
			Scope      string `json:"scope"`
			Category   string `json:"category"`
			Content    string `json:"content"`
			Importance int    `json:"importance"`
			Source     string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		item := &memport.MemoryItem{
			UserID: body.UserID, ProjectID: body.ProjectID,
			Scope: memport.Scope(body.Scope), Category: body.Category,
			Content: body.Content, Importance: body.Importance, Source: body.Source,
		}
		if item.Source == "" {
			item.Source = "api"
		}
		if err := s.app.SaveMemory(r.Context(), item); err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		observability.Global.MemoryWrites.Add(1)
		writeJSON(w, 200, map[string]any{"code": "0000", "data": item})
	default:
		writeJSON(w, 405, map[string]any{"code": "405"})
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"code": "0000", "data": observability.Global.Snapshot()})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("sessionId")
	list, err := s.app.ListAudit(r.Context(), sid, 100)
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errMap(err error) map[string]any {
	return map[string]any{"code": "400", "message": err.Error()}
}
