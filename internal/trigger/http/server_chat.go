package http

// Chat handlers: synchronous chat, SSE streaming (v1 legacy + v2 SSE runner),
// background (detached) runs, and graceful SSE shutdown.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spray272598/code-agent/internal/api/dto"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	sseinfra "github.com/spray272598/code-agent/internal/infrastructure/sse"
	"github.com/spray272598/code-agent/internal/observability"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "method not allowed")
		return
	}
	var edge dto.ChatRequest
	if !decodeJSON(w, r, &edge) {
		return
	}
	res, err := s.app.Chat(dto.ToAppChat(edge))
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	writeOK(w, dto.FromAppChat(res))
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "method not allowed")
		return
	}
	var edge dto.ChatRequest
	if !decodeJSON(w, r, &edge) {
		return
	}
	ch, sess, err := s.app.ChatStream(r.Context(), dto.ToAppChat(edge))
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}

	if s.sseHandler != nil {
		s.handleChatStreamV2(w, r, sess.ID, ch)
	} else {
		s.handleChatStreamLegacy(w, r, sess.ID, ch)
	}
}

func (s *Server) handleChatStreamV2(w http.ResponseWriter, r *http.Request, sessionID string, ch <-chan *engine.Event) {
	writer, err := s.sseHandler.NewStreamRunner(w, r, sessionID)
	if err != nil {
		observability.Errorf("sse v2 init: %v", err)
		s.handleChatStreamLegacy(w, r, sessionID, ch)
		return
	}

	writer.Start()

	s.sseWriters.Store(sessionID, writer)
	defer func() {
		s.sseWriters.Delete(sessionID)
		writer.Close()
	}()

	done := make(chan struct{})
	go func() {
		for ev := range ch {
			writer.SendEvent(ev)
		}
		close(done)
	}()

	select {
	case <-r.Context().Done():
		observability.Infof("sse v2 client disconnected: session=%s", sessionID)
	case <-done:
		if err := writer.Wait(); err != nil {
			observability.Errorf("sse v2 stream error: %v", err)
		}
	}
}

// GracefulSSEShutdown closes all active SSE connections and waits for completion.
func (s *Server) GracefulSSEShutdown(timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	type closer struct {
		runner *sseinfra.SSEStreamRunner
		done   <-chan struct{}
	}

	var closers []closer

	s.sseWriters.Range(func(key, value any) bool {
		if writer, ok := value.(*sseinfra.SSEStreamRunner); ok {
			closers = append(closers, closer{runner: writer, done: writer.DoneCh()})
		}
		return true
	})

	for _, c := range closers {
		_ = c.runner.GracefulClose(timeout)
	}

	sseinfra.SSEObserveDisconnection()
	<-deadline.C
}

func (s *Server) handleChatStreamLegacy(w http.ResponseWriter, r *http.Request, sessionID string, ch <-chan *engine.Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"code": "500", "message": "stream unsupported"})
		return
	}
	fmt.Fprintf(w, "event: session\ndata: {\"sessionId\":%q}\n\n", sessionID)
	flusher.Flush()

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

// handleChatBackground starts a detached (headless) run and returns the session
// ID immediately. The agent loop runs in the background; poll /chat or /usage
// to observe progress, and use SendControl/CancelSession to steer it.
func (s *Server) handleChatBackground(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "405", "method not allowed")
		return
	}
	var edge dto.ChatRequest
	if !decodeJSON(w, r, &edge) {
		return
	}
	sessionID, err := s.app.RunBackground(r.Context(), dto.ToAppChat(edge), func(ev *engine.Event) {
		_ = ev // background mode does not push to a client by default
	})
	if err != nil {
		writeErr(w, 400, "400", err.Error())
		return
	}
	writeOK(w, map[string]any{"sessionId": sessionID, "mode": "background"})
}
