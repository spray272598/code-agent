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
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/api/dto"
	"github.com/spray272598/code-agent/internal/application"
	"github.com/spray272598/code-agent/internal/domain/codeindex"
	"github.com/spray272598/code-agent/internal/domain/host"
	"github.com/spray272598/code-agent/internal/types/common"
	sseinfra "github.com/spray272598/code-agent/internal/infrastructure/sse"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/trigger/ws"
)

type Server struct {
	app         *application.ChatApp
	addr        string
	srv         *http.Server
	hostHub     *ws.HostHub
	sshHub      *ws.SSHTerminalHub
	bridge      *host.Bridge
	index       *codeindex.Index
	corsOrigins []string
	maxBody     int64
	sseHandler  *sseinfra.SSEHandler
	sseWriters  sync.Map
}

// defaultCORSOrigins is the safe localhost-only fallback used when WithSecurity
// is not called or config supplies no origins. Keep in sync with
// config.Default().Security.CORSOrigins.
var defaultCORSOrigins = []string{
	"http://localhost:3000", "http://127.0.0.1:3000",
	"http://localhost:8080", "http://127.0.0.1:8080",
}

// defaultMaxBodyBytes is the fallback request body limit (2 MiB).
// Keep in sync with config.Default().Security.MaxBodyBytes.
const defaultMaxBodyBytes int64 = 2 << 20

func New(app *application.ChatApp, addr string) *Server {
	return &Server{
		app:         app,
		addr:        addr,
		corsOrigins: defaultCORSOrigins,
		maxBody:     defaultMaxBodyBytes,
		sseHandler:  sseinfra.NewSSEHandler(),
	}
}

func (s *Server) WithHost(hub *ws.HostHub, bridge *host.Bridge) *Server {
	s.hostHub = hub
	s.bridge = bridge
	return s
}

func (s *Server) WithSSHHub(hub *ws.SSHTerminalHub) *Server {
	s.sshHub = hub
	return s
}

// WithIndex attaches workspace code index for HTTP search/rebuild.
func (s *Server) WithIndex(idx *codeindex.Index) *Server {
	s.index = idx
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
	mux.HandleFunc("/api/v1/chat/background", s.handleChatBackground)
	// ACP (Agent Client Protocol) compatible surface for IDEs
	s.MountACP(mux)
	mux.HandleFunc("/api/v1/tools", s.handleTools)
	mux.HandleFunc("/api/v1/permission/pending", s.handlePermPending)
	mux.HandleFunc("/api/v1/permission/approve", s.handlePermApprove)
	mux.HandleFunc("/api/v1/permission/reject", s.handlePermReject)
	mux.HandleFunc("/api/v1/skills", s.handleSkills)
	mux.HandleFunc("/api/v1/skills/install", s.handleSkillInstall)
	mux.HandleFunc("/api/v1/skills/uninstall", s.handleSkillUninstall)
	mux.HandleFunc("/api/v1/skills/reload", s.handleSkillReload)
	mux.HandleFunc("/api/v1/memory", s.handleMemory)
	mux.HandleFunc("/api/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/audit", s.handleAudit)
	mux.HandleFunc("/api/v1/blobs", s.handleBlobGet)
	mux.HandleFunc("/api/v1/usage", s.handleUsage)
	mux.HandleFunc("/api/v1/session/plan/explore", s.handlePlanExplore)
	mux.HandleFunc("/api/v1/session/plan/implement", s.handlePlanImplement)
	mux.HandleFunc("/api/v1/host/devices", s.handleHostDevices)
	mux.HandleFunc("/api/v1/session/cancel", s.handleSessionCancel)
	mux.HandleFunc("/api/v1/session/checkpoint", s.handleSessionCheckpoint)
	mux.HandleFunc("/api/v1/session/checkpoints", s.handleSessionCheckpoints)
	mux.HandleFunc("/api/v1/session/resumable", s.handleSessionResumable)
	mux.HandleFunc("/api/v1/session/resume", s.handleSessionResume)
	mux.HandleFunc("/api/v1/session/runs", s.handleSessionRuns)
	mux.HandleFunc("/api/v1/index/search", s.handleIndexSearch)
	mux.HandleFunc("/api/v1/index/rebuild", s.handleIndexRebuild)
	mux.HandleFunc("/api/v1/index/stats", s.handleIndexStats)
	mux.HandleFunc("/api/v1/ssh/connections", s.handleSSHConnections)
	mux.HandleFunc("/api/v1/ssh/health", s.handleSSHHealth)
	mux.HandleFunc("/api/v1/admin/log-level", s.handleLogLevel)
	mux.HandleFunc("/api/v1/openapi.json", s.handleOpenAPI)
	mux.HandleFunc("/docs", s.handleSwaggerUI)

	// MCP endpoints — single operator, no per-user manager factory. The host gRPC
	// / ws layer and this HTTP surface share the one Manager wired at startup.
	mux.HandleFunc("/api/v1/mcp/servers", s.handleMCPServers)
	mux.HandleFunc("/api/v1/mcp/health", s.handleMCPHealth)
	mux.HandleFunc("/api/v1/mcp/tools", s.handleMCPTools)

	mux.HandleFunc("/metrics", observability.WritePrometheus) // Prometheus scrape (auth-skipped)
	if s.hostHub != nil {
		mux.Handle("/ws/host", s.hostHub)
		log.Printf("[http] host agent ws: /ws/host?token=&deviceId=\n")
	}
	if s.sshHub != nil {
		mux.Handle("/ws/ssh", s.sshHub)
		log.Printf("[http] ssh terminal ws: /ws/ssh?token=&connection=<name>&cols=&rows=\n")
	}

	handler := s.corsMiddleware(limitBody(s.maxBody, auth(s.app, observability.AccessLog(observability.RequestIDMiddleware(mux)))))
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      5 * time.Minute, // Long enough for SSE streams, prevents slow-client resource exhaustion
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

// auth enforces the single-credential model: an API key (header X-API-Key or a
// Bearer token). When no keys are configured the gate is open (local/dev mode);
// otherwise the static key must match. There is no account system, no JWT, and
// no per-user tenant — the harness is single-operator.
func auth(app *application.ChatApp, next http.Handler) http.Handler {
	public := map[string]bool{
		"/health":             true,
		"/metrics":            true,
		"/ws/host":            true,
		"/ws/ssh":             true,
		"/api/v1/openapi.json": true,
		"/docs":               true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if public[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				key = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if app.Auth(key) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "401", "unauthorized")
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
			writeErr(w, http.StatusRequestEntityTooLarge, "413", "request body too large")
			return false
		}
		if err == io.EOF {
			writeErr(w, http.StatusBadRequest, "400", "empty body")
			return false
		}
		writeErr(w, http.StatusBadRequest, "400", "invalid json: "+msg)
		return false
	}
	return true
}

func truncateStr(s string, n int) string {
	return common.TruncateStr(s, n)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeOK unified success envelope.
func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, 200, dto.OK(data))
}

// writeErr unified error envelope.
func writeErr(w http.ResponseWriter, httpStatus int, code, message string) {
	if httpStatus <= 0 {
		httpStatus = 400
	}
	writeJSON(w, httpStatus, dto.Fail(code, message))
}

func errMap(err error) map[string]any {
	return map[string]any{"code": "400", "message": err.Error()}
}
