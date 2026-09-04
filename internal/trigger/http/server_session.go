package http

// Session handlers: session CRUD, checkpoints, cancel, resume, active runs,
// and plan mode (explore/implement).

import (
	"encoding/json"
	"net/http"
)

// operatorID is the single operator identity used by this harness. There is no
// account system and no per-user tenant; the API-key gate (see auth()) already
// authenticates the only caller, so every session/audit row is owned by it.
const operatorID = "operator"

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			ProjectID string `json:"projectId"`
			Title     string `json:"title"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		sess, err := s.app.CreateSession(operatorID, body.ProjectID, body.Title)
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
	list, err := s.app.ListSessions(operatorID)
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
}

func (s *Server) handleSessionCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
		Reason    string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.SessionID == "" {
		body.SessionID = r.URL.Query().Get("sessionId")
	}
	if body.SessionID == "" {
		writeErr(w, 400, "400", "sessionId required")
		return
	}
	ok, err := s.app.CancelSession(body.SessionID, body.Reason)
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"cancelled": ok, "sessionId": body.SessionID,
	}})
}

func (s *Server) handleSessionCheckpoint(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("sessionId")
	if sid == "" {
		writeErr(w, 400, "400", "sessionId required")
		return
	}
	snap, err := s.app.GetCheckpoint(r.Context(), sid)
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": snap, "running": s.app.IsSessionRunning(sid)})
}

func (s *Server) handleSessionCheckpoints(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := s.app.ListCheckpoints(r.Context(), status, 50)
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
}

func (s *Server) handleSessionResumable(w http.ResponseWriter, r *http.Request) {
	list := s.app.ListResumable(r.Context())
	writeJSON(w, 200, map[string]any{"code": "0000", "data": list})
}

func (s *Server) handleSessionResume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "400", "invalid body")
		return
	}
	if body.SessionID == "" {
		writeErr(w, 400, "400", "sessionId required")
		return
	}
	res, err := s.app.ResumeSession(r.Context(), body.SessionID, body.Message)
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": res})
}

func (s *Server) handleSessionRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"active": s.app.ActiveRuns(),
	}})
}

// handlePlanExplore switches an active session into the read-only plan explore
// phase (3.5 PlanMode state machine).
func (s *Server) handlePlanExplore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.SessionID == "" {
		body.SessionID = r.URL.Query().Get("sessionId")
	}
	if body.SessionID == "" {
		writeErr(w, 400, "400", "sessionId required")
		return
	}
	ok := s.app.EnterPlanMode(body.SessionID)
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"explore": ok}})
}

// handlePlanImplement exits the plan phase into the writable implement phase.
func (s *Server) handlePlanImplement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.SessionID == "" {
		body.SessionID = r.URL.Query().Get("sessionId")
	}
	if body.SessionID == "" {
		writeErr(w, 400, "400", "sessionId required")
		return
	}
	ok := s.app.ExitPlanMode(body.SessionID)
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"implement": ok}})
}
