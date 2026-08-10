package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	sshmodel "github.com/spray272598/code-agent/internal/domain/ssh/model"
)

func (s *Server) handleSSHConnections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
		conns, err := s.app.ListSSHConnections(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errMap(err))
			return
		}
		// mask sensitive fields
		for _, c := range conns {
			c.Password = "***"
			c.PrivateKey = ""
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": "0000", "data": conns})

	case http.MethodPost:
		var cfg sshmodel.ConnectionConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, errMap(err))
			return
		}
		if cfg.Name == "" || cfg.Host == "" || cfg.Username == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "400", "message": "name, host, username are required"})
			return
		}
		if err := s.app.InstallSSH(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMap(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": "0000", "data": map[string]any{"ok": true, "id": cfg.ID, "name": cfg.Name}})

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "400", "message": "id required"})
			return
		}
		if err := s.app.DeleteSSHConnection(ctx, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, errMap(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": "0000", "data": map[string]bool{"ok": true}})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "405", "message": "method not allowed"})
	}
}

func (s *Server) handleSSHHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "405", "message": "GET only"})
		return
	}
	health := s.app.SSHHealth()
	writeJSON(w, http.StatusOK, map[string]any{"code": "0000", "data": health})
}
