package http

// Memory, metrics, audit, blobs, usage, host devices, and code-index handlers.

import (
	"encoding/json"
	"fmt"
	"net/http"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tenant"
	"github.com/spray272598/code-agent/internal/observability"
)

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
		observability.Current().AddMemoryWrites(1)
		writeJSON(w, 200, map[string]any{"code": "0000", "data": item})
	default:
		writeJSON(w, 405, map[string]any{"code": "405"})
	}
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"code": "0000", "data": s.app.ListTools()})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"code": "0000", "data": observability.Current().Snapshot()})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	// Sprint 1.6 + 1.7: tenant scoping comes from ctx. Cross-tenant reads return
	// nothing because ListForUser refuses to run when ctx has no tenant.
	t, ok := tenant.From(r.Context())
	if !ok || t.UserID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "401", "message": "unauthenticated"})
		return
	}
	sid := r.URL.Query().Get("sessionId")
	list, err := s.app.ListAuditCtx(r.Context(), sid, 100)
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

// handleUsage returns the current resource-consumption snapshot (3.5 usage
// panel). Accepts optional ?user= and ?session= query params.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "405", "GET only")
		return
	}
	userID := r.URL.Query().Get("user")
	sessionID := r.URL.Query().Get("session")
	if userID == "" {
		userID = "default"
	}
	u := s.app.UsageSnapshot(r.Context(), userID, sessionID)
	writeJSON(w, 200, map[string]any{"code": "0000", "data": u})
}

func (s *Server) handleIndexSearch(w http.ResponseWriter, r *http.Request) {
	if s.index == nil {
		writeErr(w, 503, "503", "index unavailable")
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		q = r.URL.Query().Get("query")
	}
	if q == "" {
		writeErr(w, 400, "400", "q required")
		return
	}
	k := 8
	if v := r.URL.Query().Get("top_k"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &k); n == 1 && err == nil && k > 0 {
			// ok
		}
	}
	if s.index.Stats().Files == 0 {
		_, _ = s.index.Build(r.Context())
	}
	hits := s.index.Search(q, k)
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"query": q, "hits": hits, "stats": s.index.Stats(),
	}})
}

func (s *Server) handleIndexRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	if s.index == nil {
		writeErr(w, 503, "503", "index unavailable")
		return
	}
	st, err := s.index.Build(r.Context())
	if err != nil {
		writeErr(w, 500, "500", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": st})
}

func (s *Server) handleIndexStats(w http.ResponseWriter, r *http.Request) {
	if s.index == nil {
		writeErr(w, 503, "503", "index unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": s.index.Stats()})
}
