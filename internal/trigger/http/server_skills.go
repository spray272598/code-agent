package http

// Skills handlers: list, install from path, uninstall, and reload.

import (
	"encoding/json"
	"net/http"
)

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
