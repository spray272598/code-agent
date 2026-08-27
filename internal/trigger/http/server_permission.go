package http

// Tool permission handlers: pending list, approve (with optional inline resume),
// and reject.

import (
	"net/http"

	"github.com/spray272598/code-agent/internal/api/dto"
	"github.com/spray272598/code-agent/internal/application"
)

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
	var body dto.PermissionApproveRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID == "" {
		writeErr(w, 400, "400", "id required")
		return
	}
	if body.Scope == "" {
		body.Scope = "once"
	}
	p, err := s.app.Permission().Approve(body.ID, body.Scope)
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	out := map[string]any{"approved": true, "pending": p, "inline": body.Continue}
	// Inline continue: approve + resume agent in one round-trip (Claude Code-like UX)
	if body.Continue {
		sid := body.SessionID
		if sid == "" && p != nil {
			sid = p.SessionID
		}
		msg := body.InlineMessage
		if msg == "" {
			msg = "继续"
		}
		res, err := s.app.Chat(application.ChatRequest{SessionID: sid, UserID: body.UserID, Message: msg})
		if err != nil {
			out["continueError"] = err.Error()
		} else {
			out["chat"] = dto.FromAppChat(res)
		}
	}
	writeOK(w, out)
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
