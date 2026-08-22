package http

import (
	"math/rand"
	"net/http"
	"strings"

	"github.com/spray272598/code-agent/internal/api/dto"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

// MountACP exposes an Agent Client Protocol (ACP)-compatible surface so IDEs
// (VS Code, etc.) can drive sessions. It is a thin adapter over ChatApp's
// existing session/stream/control APIs — no new agent logic, just an
// ACP-shaped JSON contract. Endpoints:
//   POST /acp/sessions              -> create session (returns {sessionId})
//   POST /acp/sessions/{id}/prompt  -> run a prompt (background, returns 202)
//   POST /acp/sessions/{id}/cancel  -> cancel
//   POST /acp/sessions/{id}/control -> send ControlSignal (replan/pause/...)
//   GET  /acp/sessions/{id}         -> usage/status snapshot
func (s *Server) MountACP(mux *http.ServeMux) {
	mux.HandleFunc("/acp/sessions", s.acpCreateSession)
	mux.HandleFunc("/acp/sessions/", s.acpSessionDispatcher)
}

func (s *Server) acpCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	var body struct {
		UserID string `json:"userId"`
	}
	_ = decodeJSON(w, r, &body)
	if body.UserID == "" {
		body.UserID = "acp-default"
	}
	// ACP sessions are created lazily: the IDE prompts with a sessionId of its
	// choosing, or we mint one. No agent run is started until /prompt.
	sid := "acp-" + randHex(8)
	writeOK(w, map[string]any{"sessionId": sid, "jsonrpc": "2.0"})
}

func (s *Server) acpSessionDispatcher(w http.ResponseWriter, r *http.Request) {
	id := acpSessionID(r.URL.Path)
	if id == "" {
		writeErr(w, 400, "400", "session id required")
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/prompt"):
		s.acpPrompt(w, r, id)
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		s.acpCancel(w, r, id)
	case strings.HasSuffix(r.URL.Path, "/control"):
		s.acpControl(w, r, id)
	default:
		s.acpStatus(w, r, id)
	}
}

func (s *Server) acpPrompt(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	var body struct {
		UserID string `json:"userId"`
		Prompt string `json:"prompt"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Prompt == "" {
		writeErr(w, 400, "400", "prompt required")
		return
	}
	_, err := s.app.RunBackground(r.Context(), dto.ToAppChat(dto.ChatRequest{
		UserID: body.UserID, SessionID: id, Message: body.Prompt, AutoApprove: true,
	}), nil)
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	w.WriteHeader(202)
	writeOK(w, map[string]any{"sessionId": id, "accepted": true})
}

func (s *Server) acpCancel(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	ok, _ := s.app.CancelSession(id, "acp cancel")
	writeOK(w, map[string]any{"cancelled": ok})
}

func (s *Server) acpControl(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "POST only")
		return
	}
	var body struct {
		Signal string `json:"signal"`
		Goal   string `json:"goal"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sig := acpSignal(body.Signal)
	ok := s.app.SendControl(id, sig, body.Goal)
	writeOK(w, map[string]any{"delivered": ok})
}

func (s *Server) acpStatus(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "405", "GET only")
		return
	}
	u := s.app.UsageSnapshot(r.Context(), "default", id)
	writeOK(w, map[string]any{"sessionId": id, "usage": u})
}

// acpSignal maps an ACP signal name to our engine ControlSignal.
func acpSignal(name string) engine.ControlSignal {
	switch name {
	case "replan":
		return engine.ControlReplan
	case "replan_goal", "replanGoal":
		return engine.ControlReplanWithGoal
	case "pause":
		return engine.ControlPause
	case "resume":
		return engine.ControlResume
	case "interrupt", "cancel":
		return engine.ControlInterrupt
	case "plan_explore", "explore":
		return engine.ControlPlanExplore
	case "plan_implement", "implement":
		return engine.ControlPlanImplement
	default:
		return engine.ControlNone
	}
}

func acpSessionID(path string) string {
	const prefix = "/acp/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	for _, suf := range []string{"/prompt", "/cancel", "/control"} {
		if strings.HasSuffix(rest, suf) {
			rest = rest[:len(rest)-len(suf)]
			break
		}
	}
	return rest
}

func randHex(n int) string {
	const chars = "abcdef0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
