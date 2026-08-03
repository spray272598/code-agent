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

	s.srv = &http.Server{Addr: s.addr, Handler: cors(auth(s.app, mux)), ReadHeaderTimeout: 10 * time.Second}
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errMap(err error) map[string]any {
	return map[string]any{"code": "400", "message": err.Error()}
}
