package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/application"
	"github.com/spray272598/code-agent/internal/domain/host"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/trigger/ws"
)

type Server struct {
	app         *application.ChatApp
	addr        string
	srv         *http.Server
	hostHub     *ws.HostHub
	bridge      *host.Bridge
	corsOrigins []string
	maxBody     int64
}

func New(app *application.ChatApp, addr string) *Server {
	return &Server{
		app: app, addr: addr,
		// safe default: localhost only (never bare * with credentials)
		corsOrigins: []string{"http://localhost:3000", "http://127.0.0.1:3000", "http://localhost:8080", "http://127.0.0.1:8080"},
		maxBody:     2 << 20,
	}
}

func (s *Server) WithHost(hub *ws.HostHub, bridge *host.Bridge) *Server {
	s.hostHub = hub
	s.bridge = bridge
	return s
}

// WithSecurity sets CORS allowlist and max body size.
func (s *Server) WithSecurity(corsOrigins []string, maxBody int64) *Server {
	if len(corsOrigins) > 0 {
		s.corsOrigins = corsOrigins
	}
	if maxBody > 0 {
		s.maxBody = maxBody
	}
	return s
}

func (s *Server) Start() error {
	return s.StartTLS("", "")
}

// StartTLS serves HTTPS when certFile and keyFile are non-empty; otherwise HTTP.
func (s *Server) StartTLS(certFile, keyFile string) error {
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
	mux.HandleFunc("/api/v1/blobs", s.handleBlobGet)
	mux.HandleFunc("/api/v1/host/devices", s.handleHostDevices)
	mux.HandleFunc("/api/v1/admin/log-level", s.handleLogLevel)
	mux.HandleFunc("/api/v1/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/docs", s.handleSwaggerUI)
	mux.HandleFunc("/metrics", observability.WritePrometheus) // Prometheus scrape (auth-skipped)
	if s.hostHub != nil {
		mux.Handle("/ws/host", s.hostHub)
		log.Printf("[http] host agent ws: /ws/host?token=&deviceId=\n")
	}

	handler := s.corsMiddleware(limitBody(s.maxBody, auth(s.app, observability.AccessLog(observability.RequestIDMiddleware(mux)))))
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// WriteTimeout left 0 for long SSE streams
	}
	if certFile != "" && keyFile != "" {
		if err := validateTLSFiles(certFile, keyFile); err != nil {
			return fmt.Errorf("tls precheck: %w", err)
		}
		log.Printf("[http] listening TLS on %s cert=%s\n", s.addr, certFile)
		return s.srv.ListenAndServeTLS(certFile, keyFile)
	}
	log.Printf("[http] listening on %s (cors_origins=%d)\n", s.addr, len(s.corsOrigins))
	return s.srv.ListenAndServe()
}

func validateTLSFiles(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); err != nil {
		return fmt.Errorf("cert: %w", err)
	}
	if _, err := os.Stat(keyFile); err != nil {
		return fmt.Errorf("key: %w", err)
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return fmt.Errorf("load pair: %w", err)
	}
	return nil
}

func limitBody(max int64, next http.Handler) http.Handler {
	if max <= 0 {
		max = 2 << 20
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func auth(app *application.ChatApp, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// public / health / metrics / openapi / host ws (ws auth itself)
		switch r.URL.Path {
		case "/health", "/metrics", "/ws/host", "/api/v1/openapi.json", "/docs":
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

// corsMiddleware applies an origin allowlist. Never reflects arbitrary Origin with credentials.
// Use cors_origins: ["*"] only for pure public demos (still no credentials header).
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allow := map[string]bool{}
	star := false
	for _, o := range s.corsOrigins {
		o = strings.TrimSpace(o)
		if o == "*" {
			star = true
			continue
		}
		if o != "" {
			allow[o] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if star {
				// demo mode: allow any origin but do NOT enable credentials
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if allow[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			// unknown origin: no ACAO → browser blocks credentialed cross-origin
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// decodeJSON reads JSON body with clear 400 messages (incl. body-too-large).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "http: request body too large") || strings.Contains(msg, "request body too large") {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"code": "413", "message": "request body too large"})
			return false
		}
		if err == io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "400", "message": "empty body"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "400", "message": "invalid json: " + msg})
		return false
	}
	return true
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
		if !decodeJSON(w, r, &body) {
			return
		}
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
		writeJSON(w, 405, map[string]any{"code": "405", "message": "method not allowed"})
		return
	}
	var req application.ChatRequest
	if !decodeJSON(w, r, &req) {
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
		writeJSON(w, 405, map[string]any{"code": "405", "message": "method not allowed"})
		return
	}
	var req application.ChatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// bind to request context → client disconnect cancels agent loop
	ch, sess, err := s.app.ChatStream(r.Context(), req)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"code": "500", "message": "stream unsupported"})
		return
	}
	fmt.Fprintf(w, "event: session\ndata: {\"sessionId\":%q}\n\n", sess.ID)
	flusher.Flush()

	// heartbeat keeps proxies from closing idle SSE connections
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix())
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev == nil {
				continue
			}
			b, err := json.Marshal(ev)
			if err != nil {
				observability.Warnf("sse marshal: %v", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, string(b))
			flusher.Flush()
		}
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
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID == "" {
		writeJSON(w, 400, map[string]any{"code": "400", "message": "id required"})
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
	if !decodeJSON(w, r, &body) {
		return
	}
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

func (s *Server) handleHostDevices(w http.ResponseWriter, r *http.Request) {
	if s.bridge == nil {
		writeJSON(w, 200, map[string]any{"code": "0000", "data": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"online":  s.bridge.OnlineCount(),
		"devices": s.bridge.ListDevices(),
		"ws":      "/ws/host?token=<apiKey>&deviceId=local-dev",
	}})
}

func (s *Server) handleBlobGet(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeJSON(w, 400, map[string]any{"message": "key required"})
		return
	}
	data, err := s.app.GetBlob(r.Context(), key)
	if err != nil {
		writeJSON(w, 404, errMap(err))
		return
	}
	// raw download when format=raw
	if r.URL.Query().Get("format") == "raw" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"key": key, "size": len(data),
		"preview": truncateStr(string(data), 500),
	}})
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errMap(err error) map[string]any {
	return map[string]any{"code": "400", "message": err.Error()}
}

func (s *Server) handleLogLevel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"level": observability.LogLevel()}})
	case http.MethodPost, http.MethodPut:
		var body struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		observability.SetLogLevel(body.Level)
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"level": observability.LogLevel()}})
	default:
		writeJSON(w, 405, map[string]any{"code": "405"})
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(openAPISpec))
}

func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Code-Agent API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"/>
</head><body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
SwaggerUIBundle({url:'/api/v1/openapi.json', dom_id:'#swagger-ui'});
</script>
</body></html>`))
}

// Minimal OpenAPI 3.0 for interview / client generation.
const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {"title": "Code-Agent API", "version": "1.0.0",
    "description": "Claude Code-like coding agent: chat, tools, MCP, skills, memory, permissions."},
  "servers": [{"url": "/"}],
  "components": {
    "securitySchemes": {
      "ApiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
      "BearerAuth": {"type": "http", "scheme": "bearer"}
    }
  },
  "security": [{"ApiKeyAuth": []}, {"BearerAuth": []}],
  "paths": {
    "/health": {"get": {"summary": "Health", "security": [], "responses": {"200": {"description": "ok"}}}},
    "/api/v1/session": {
      "post": {"summary": "Create session", "responses": {"200": {"description": "session"}}},
      "get": {"summary": "Get session by id query", "parameters": [{"name":"id","in":"query","schema":{"type":"string"}}], "responses": {"200": {"description": "ok"}}}
    },
    "/api/v1/session/list": {"get": {"summary": "List sessions", "parameters": [{"name":"userId","in":"query","schema":{"type":"string"}}], "responses": {"200": {"description": "ok"}}}},
    "/api/v1/chat": {"post": {"summary": "Chat (sync)", "requestBody": {"content": {"application/json": {"schema": {"type":"object","properties": {
      "sessionId":{"type":"string"},"userId":{"type":"string"},"message":{"type":"string"},"autoApprove":{"type":"boolean"}
    }}}}}, "responses": {"200": {"description": "chat result"}}}},
    "/api/v1/chat/stream": {"post": {"summary": "Chat SSE stream", "responses": {"200": {"description": "text/event-stream"}}}},
    "/api/v1/tools": {"get": {"summary": "List tools", "responses": {"200": {"description": "ok"}}}},
    "/api/v1/permission/pending": {"get": {"summary": "Pending permissions", "responses": {"200": {"description": "ok"}}}},
    "/api/v1/permission/approve": {"post": {"summary": "Approve permission", "responses": {"200": {"description": "ok"}}}},
    "/api/v1/permission/reject": {"post": {"summary": "Reject permission", "responses": {"200": {"description": "ok"}}}},
    "/api/v1/mcp/servers": {"get": {"summary": "MCP servers"}, "post": {"summary": "Install/update MCP"}},
    "/api/v1/mcp/health": {"get": {"summary": "MCP health"}},
    "/api/v1/mcp/tools": {"get": {"summary": "MCP tools"}},
    "/api/v1/skills": {"get": {"summary": "List skills"}},
    "/api/v1/skills/install": {"post": {"summary": "Install skill"}},
    "/api/v1/skills/uninstall": {"post": {"summary": "Uninstall skill"}},
    "/api/v1/skills/reload": {"post": {"summary": "Reload skills"}},
    "/api/v1/memory": {"get": {"summary": "List/search memory"}, "post": {"summary": "Save memory"}},
    "/api/v1/metrics": {"get": {"summary": "JSON metrics"}},
    "/api/v1/audit": {"get": {"summary": "Audit log"}},
    "/api/v1/blobs": {"get": {"summary": "Get blob by key"}},
    "/api/v1/host/devices": {"get": {"summary": "Host agents"}},
    "/api/v1/admin/log-level": {"get": {"summary": "Get log level"}, "post": {"summary": "Set log level"}},
    "/metrics": {"get": {"summary": "Prometheus text", "security": []}},
    "/docs": {"get": {"summary": "Swagger UI", "security": []}}
  }
}`
