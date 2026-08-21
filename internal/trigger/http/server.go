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

	"github.com/spray272598/code-agent/internal/api/dto"
	"github.com/spray272598/code-agent/internal/application"
	authdomain "github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/codeindex"
	"github.com/spray272598/code-agent/internal/domain/host"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tenant"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/trigger/ws"
	"go.opentelemetry.io/otel/trace"
)

type Server struct {
	app         *application.ChatApp
	addr        string
	srv         *http.Server
	hostHub     *ws.HostHub
	bridge      *host.Bridge
	index       *codeindex.Index
	corsOrigins []string
	maxBody     int64
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
	}
}

func (s *Server) WithHost(hub *ws.HostHub, bridge *host.Bridge) *Server {
	s.hostHub = hub
	s.bridge = bridge
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
	mux.HandleFunc("/api/v1/tools", s.handleTools)
	mux.HandleFunc("/api/v1/permission/pending", s.handlePermPending)
	mux.HandleFunc("/api/v1/permission/approve", s.handlePermApprove)
	mux.HandleFunc("/api/v1/permission/reject", s.handlePermReject)
	// MCP endpoints — Sprint 1.6: per-user. The factory resolves the Manager for
	// the authenticated tenant; cross-tenant reads return ErrTenantMismatch.
	mux.HandleFunc("/api/v1/mcp/servers", s.authJWT(s.handleMCPServers))
	mux.HandleFunc("/api/v1/mcp/health", s.authJWT(s.handleMCPHealth))
	mux.HandleFunc("/api/v1/mcp/tools", s.authJWT(s.handleMCPTools))
	mux.HandleFunc("/api/v1/skills", s.handleSkills)
	mux.HandleFunc("/api/v1/skills/install", s.handleSkillInstall)
	mux.HandleFunc("/api/v1/skills/uninstall", s.handleSkillUninstall)
	mux.HandleFunc("/api/v1/skills/reload", s.handleSkillReload)
	mux.HandleFunc("/api/v1/memory", s.handleMemory)
	mux.HandleFunc("/api/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/audit", s.authJWT(s.handleAudit))
	mux.HandleFunc("/api/v1/blobs", s.handleBlobGet)
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

	// auth (Sprint 1.2): public endpoints
	mux.HandleFunc("/api/v1/auth/signup", s.handleSignup)
	mux.HandleFunc("/api/v1/auth/verify", s.handleVerify)
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/api/v1/auth/forgot-password", s.handleForgotPassword)
	mux.HandleFunc("/api/v1/auth/reset-password", s.handleResetPassword)
	mux.HandleFunc("/api/v1/me", s.authJWT(s.handleMe))
	mux.HandleFunc("/api/v1/me/profile", s.authJWT(s.handleUpdateProfile))
	mux.HandleFunc("/api/v1/me/password", s.authJWT(s.handleChangePassword))

	// device authorization (RFC8628, Sprint 1.4)
	mux.HandleFunc("/api/v1/device/code", s.handleDeviceCode)
	mux.HandleFunc("/api/v1/device/token", s.handleDeviceToken)
	mux.HandleFunc("/api/v1/device/approve", s.authJWT(s.handleDeviceApprove))
	mux.HandleFunc("/metrics", observability.WritePrometheus) // Prometheus scrape (auth-skipped)
	if s.hostHub != nil {
		mux.Handle("/ws/host", s.hostHub)
		log.Printf("[http] host agent ws: /ws/host?token=&deviceId=\n")
	}

	handler := s.corsMiddleware(limitBody(s.maxBody, auth(s.app, observability.AccessLog(observability.RequestIDMiddleware(observability.RequestSpanMiddleware(mux))))))
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
		case "/health", "/metrics", "/ws/host", "/api/v1/openapi.json", "/docs",
		"/api/v1/auth/signup", "/api/v1/auth/verify", "/api/v1/auth/login", "/api/v1/auth/refresh",
		"/api/v1/auth/forgot-password", "/api/v1/auth/reset-password",
		"/api/v1/device/code", "/api/v1/device/token":
			next.ServeHTTP(w, r)
			return
		}
		// 1) Bearer JWT is the primary credential (Sprint 1.3+). The gateway only
		//    decides allow/deny; downstream authJWT re-validates and injects the
		//    principal for /api/v1/me and /api/v1/device/approve.
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			if tok := app.TokenService(); tok != nil {
				if _, err := tok.Validate(strings.TrimPrefix(h, "Bearer ")); err == nil {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		// 2) API key fallback (dev / host-agent). With no keys configured this opens
		//    the gate (dev mode); in production it validates the static key.
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

// ---- auth handlers (Sprint 1.2) ----

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := svc.Signup(r.Context(), body.Email, body.Password, body.DisplayName)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"userId":  u.ID,
		"email":   u.Email,
		"status":  u.Status,
		"message": "verification email sent",
	}})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := svc.VerifyEmail(r.Context(), body.Token)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"userId": u.ID,
		"email":  u.Email,
		"status": u.Status,
	}})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := svc.AuthenticatePassword(r.Context(), body.Email, body.Password)
	if err != nil {
		writeJSON(w, 401, errMap(err))
		return
	}
	tok := s.app.TokenService()
	if tok == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "token service unavailable"})
		return
	}
	access, refresh, err := tok.IssuePair(r.Context(), u, "")
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"accessToken":  access,
		"refreshToken": refresh,
		"tokenType":    "Bearer",
		"expiresIn":    900,
		"user": map[string]any{
			"userId": u.ID, "email": u.Email,
			"role": u.Role, "emailVerified": u.EmailVerified, "status": u.Status,
		},
	}})
}

// handleRefresh rotates an opaque refresh token into a fresh access+refresh pair.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	tok := s.app.TokenService()
	if tok == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "token service unavailable"})
		return
	}
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	access, refresh, err := tok.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		writeJSON(w, 401, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"accessToken":  access,
		"refreshToken": refresh,
		"tokenType":    "Bearer",
		"expiresIn":    900,
	}})
}

// handleMe returns the authenticated principal plus the freshest profile from
// the user store (requires a valid Bearer JWT).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := authdomain.PrincipalFrom(r.Context())
	if p == nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": "unauthenticated"})
		return
	}
	out := map[string]any{
		"userId": p.UserID, "deviceId": p.DeviceID,
		"role": p.Role, "email": p.Email,
	}
	svc := s.app.AuthService()
	if svc != nil {
		if u, err := svc.GetUser(r.Context(), p.UserID); err == nil && u != nil {
			out["displayName"] = u.DisplayName
			out["emailVerified"] = u.EmailVerified
			out["status"] = u.Status
			out["role"] = u.Role
			out["createdAt"] = u.CreatedAt.UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": out})
}

// handleUpdateProfile updates the display name of the authenticated user.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	p := authdomain.PrincipalFrom(r.Context())
	if p == nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": "unauthenticated"})
		return
	}
	var body struct {
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	u, err := svc.UpdateProfile(r.Context(), p.UserID, body.DisplayName)
	if err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"userId": u.ID, "displayName": u.DisplayName, "email": u.Email, "role": u.Role,
	}})
}

// handleChangePassword verifies the current password and sets a new one.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	p := authdomain.PrincipalFrom(r.Context())
	if p == nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": "unauthenticated"})
		return
	}
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	if err := svc.ChangePassword(r.Context(), p.UserID, body.OldPassword, body.NewPassword); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"ok": true}})
}

// handleForgotPassword issues a password reset email for the given email.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	if err := svc.RequestPasswordReset(r.Context(), body.Email); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	// Always report success to avoid leaking which accounts exist.
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"ok": true}})
}

// handleResetPassword consumes a reset token and sets a new password.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	svc := s.app.AuthService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "auth service unavailable"})
		return
	}
	if err := svc.ResetPassword(r.Context(), body.Token, body.NewPassword); err != nil {
		writeJSON(w, 400, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"ok": true}})
}

// authJWT validates the Bearer access token and injects the principal into the
// request context for downstream handlers.
func (s *Server) authJWT(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := s.app.TokenService()
		if tok == nil {
			writeJSON(w, 503, map[string]any{"code": "503", "message": "token service unavailable"})
			return
		}
		authz := r.Header.Get("Authorization")
		if len(authz) < 7 || authz[:7] != "Bearer " {
			writeJSON(w, 401, map[string]any{"code": "401", "message": "missing bearer token"})
			return
		}
		claims, err := tok.Validate(authz[7:])
		if err != nil {
			writeJSON(w, 401, map[string]any{"code": "401", "message": "invalid token"})
			return
		}
		p := &authdomain.Principal{
			UserID:   claims.Sub,
			DeviceID: claims.DID,
			Role:     claims.Role,
			Email:    claims.Email,
		}
		// Sprint 1.8: tag the request span with multi-tenant identifiers so every
		// downstream span inherits them via the active trace context.
		observability.SetTenantAttrs(trace.SpanFromContext(r.Context()), p)
		next(w, r.WithContext(authdomain.WithPrincipal(r.Context(), p)))
	}
}

// ---- RFC8628 device authorization handlers (Sprint 1.4) ----

// handleDeviceCode is the device authorization request: the device obtains a
// device_code (kept secret on the device) and a user_code (shown to the user).
func (s *Server) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	svc := s.app.DeviceService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "device auth unavailable"})
		return
	}
	var body struct {
		ClientID   string `json:"client_id"`
		Scope      string `json:"scope"`
		DeviceName string `json:"device_name"`
		Platform   string `json:"platform"`
		UserAgent  string `json:"user_agent"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	res, err := svc.StartAuthorization(r.Context(), application.DeviceAuthParams{
		ClientID: body.ClientID, Scope: body.Scope, DeviceName: body.DeviceName,
		Platform: body.Platform, UserAgent: body.UserAgent,
	})
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
		"deviceCode":      res.DeviceCode,
		"userCode":        res.UserCode,
		"verificationUri": res.VerificationURI,
		"expiresIn":       res.ExpiresIn,
		"interval":        res.Interval,
	}})
}

// handleDeviceToken is the RFC8628 polling endpoint the device calls on a fixed
// interval until the user approves (or the code expires).
func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	svc := s.app.DeviceService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "device auth unavailable"})
		return
	}
	var body struct {
		GrantType  string `json:"grant_type"`
		DeviceCode string `json:"device_code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.GrantType != "urn:ietf:params:oauth:grant-type:device_code" {
		writeJSON(w, 400, map[string]any{"code": "400", "message": "unsupported_grant_type"})
		return
	}
	out, err := svc.Poll(r.Context(), body.DeviceCode)
	if err != nil {
		writeJSON(w, 500, errMap(err))
		return
	}
	switch out.Status {
	case application.PollApproved:
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{
			"accessToken":  out.AccessToken,
			"refreshToken": out.RefreshToken,
			"tokenType":    out.TokenType,
			"expiresIn":    out.ExpiresIn,
		}})
	case application.PollPending:
		writeJSON(w, 400, map[string]any{"code": "400", "message": out.OAuthError, "data": map[string]any{
			"status": out.Status, "oauthError": out.OAuthError,
		}})
	default:
		writeJSON(w, 400, map[string]any{"code": "400", "message": out.OAuthError, "data": map[string]any{
			"status": out.Status, "oauthError": out.OAuthError,
		}})
	}
}

// handleDeviceApprove is called by the web SPA / browser (an authenticated user
// with a valid Bearer JWT) to approve or deny a pending device by its user_code.
func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"code": "405"})
		return
	}
	p := authdomain.PrincipalFrom(r.Context())
	if p == nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": "unauthenticated"})
		return
	}
	svc := s.app.DeviceService()
	if svc == nil {
		writeJSON(w, 503, map[string]any{"code": "503", "message": "device auth unavailable"})
		return
	}
	var body struct {
		UserCode string `json:"user_code"`
		Deny     bool   `json:"deny"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var err error
	if body.Deny {
		err = svc.Deny(r.Context(), body.UserCode)
	} else {
		err = svc.Approve(r.Context(), body.UserCode, p.UserID)
	}
	if err != nil {
		status := http.StatusBadRequest
		switch err {
		case application.ErrDeviceNotFound:
			status = http.StatusNotFound
		case application.ErrDeviceAlreadyApproved:
			status = http.StatusConflict
		case application.ErrDeviceExpired:
			status = http.StatusGone
		}
		writeJSON(w, status, map[string]any{"code": fmt.Sprintf("%d", status), "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "message": "ok"})
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
	// bind to request context → client disconnect cancels agent loop
	ch, sess, err := s.app.ChatStream(r.Context(), dto.ToAppChat(edge))
	if err != nil {
		writeErr(w, 400, "400", err.Error())
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

func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	// authJWT already ran; principal is on ctx. Use the per-user factory so
	// user A never sees user B's server list.
	m, err := s.app.MCPFor(r.Context())
	if err != nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": err.Error()})
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
	// use type from domain model via bootstrap-installed factory — dynamic import
	return s.app.InstallMCP(ctx, name, transport, command, args, env, url, enabled, timeout)
}

func (s *Server) handleMCPHealth(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.MCPFor(r.Context())
	if err != nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"code": "0000", "data": m.Health(r.Context())})
}

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.MCPFor(r.Context())
	if err != nil {
		writeJSON(w, 401, map[string]any{"code": "401", "message": err.Error()})
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
		observability.Current().AddMemoryWrites(1)
		writeJSON(w, 200, map[string]any{"code": "0000", "data": item})
	default:
		writeJSON(w, 405, map[string]any{"code": "405"})
	}
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

// OpenAPI 3.0 aligned with handlers + dto package (envelope code/message/data).
const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {"title": "Code-Agent API", "version": "1.1.0",
    "description": "Coding agent API. Success: {code:0000,data}. Error: {code,message}. Auth: X-API-Key or Bearer."},
  "servers": [{"url": "/"}],
  "components": {
    "securitySchemes": {
      "ApiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
      "BearerAuth": {"type": "http", "scheme": "bearer"}
    },
    "schemas": {
      "Envelope": {"type":"object","properties":{"code":{"type":"string"},"message":{"type":"string"},"data":{}}},
      "ChatRequest": {"type":"object","required":["message"],"properties":{
        "sessionId":{"type":"string"},"userId":{"type":"string"},"projectId":{"type":"string"},
        "message":{"type":"string"},"autoApprove":{"type":"boolean"}}},
      "PermissionApprove": {"type":"object","required":["id"],"properties":{
        "id":{"type":"string"},"scope":{"type":"string","enum":["once","session","always"]},
        "continue":{"type":"boolean","description":"inline resume agent after approve"},
        "sessionId":{"type":"string"},"userId":{"type":"string"},"inlineMessage":{"type":"string"}}}
    }
  },
  "security": [{"ApiKeyAuth": []}, {"BearerAuth": []}],
  "paths": {
    "/health": {"get": {"summary": "Health", "security": [], "responses": {"200": {"description": "ok"}}}},
    "/api/v1/session": {
      "post": {"summary": "Create session", "responses": {"200": {"description": "envelope+sessionId"}}},
      "get": {"summary": "Get session", "parameters": [{"name":"sessionId","in":"query","schema":{"type":"string"}}], "responses": {"200": {"description": "ok"}}}
    },
    "/api/v1/session/list": {"get": {"summary": "List sessions", "parameters": [{"name":"userId","in":"query","schema":{"type":"string"}}], "responses": {"200": {"description": "ok"}}}},
    "/api/v1/chat": {"post": {"summary": "Chat sync", "requestBody": {"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/ChatRequest"}}}},
      "responses": {"200": {"description": "ChatResponse in data"}, "400": {"description": "error envelope"}}}},
    "/api/v1/chat/stream": {"post": {"summary": "Chat SSE (heartbeat : ping)", "requestBody": {"content":{"application/json":{"schema":{"$ref":"#/components/schemas/ChatRequest"}}}},
      "responses": {"200": {"description": "text/event-stream"}}}},
    "/api/v1/tools": {"get": {"summary": "List tools"}},
    "/api/v1/permission/pending": {"get": {"summary": "Pending permissions", "parameters":[{"name":"sessionId","in":"query","schema":{"type":"string"}}]}},
    "/api/v1/permission/approve": {"post": {"summary": "Approve (+ optional inline continue)", "requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/PermissionApprove"}}}}}},
    "/api/v1/permission/reject": {"post": {"summary": "Reject permission"}},
    "/api/v1/mcp/servers": {"get": {"summary": "MCP health list"}, "post": {"summary": "Install/update MCP"}, "delete": {"summary": "Remove MCP"}},
    "/api/v1/mcp/health": {"get": {"summary": "MCP health"}},
    "/api/v1/mcp/tools": {"get": {"summary": "MCP tools"}},
    "/api/v1/skills": {"get": {"summary": "List skills"}},
    "/api/v1/skills/install": {"post": {"summary": "Install skill from path"}},
    "/api/v1/skills/uninstall": {"post": {"summary": "Uninstall skill"}},
    "/api/v1/skills/reload": {"post": {"summary": "Reload skills"}},
    "/api/v1/memory": {"get": {"summary": "List/search memory"}, "post": {"summary": "Save memory"}},
    "/api/v1/metrics": {"get": {"summary": "JSON metrics"}},
    "/api/v1/audit": {"get": {"summary": "Audit log", "parameters":[{"name":"sessionId","in":"query","schema":{"type":"string"}}]}},
    "/api/v1/blobs": {"get": {"summary": "Get blob", "parameters":[{"name":"key","in":"query","required":true,"schema":{"type":"string"}}]}},
    "/api/v1/host/devices": {"get": {"summary": "Host agents online"}},
    "/api/v1/session/cancel": {"post": {"summary": "Cancel active agent run + checkpoint"}},
    "/api/v1/session/checkpoint": {"get": {"summary": "Get session checkpoint", "parameters":[{"name":"sessionId","in":"query","required":true,"schema":{"type":"string"}}]}},
    "/api/v1/session/checkpoints": {"get": {"summary": "List checkpoints", "parameters":[{"name":"status","in":"query","schema":{"type":"string"}}]}},
    "/api/v1/session/runs": {"get": {"summary": "Active in-process runs"}},
    "/api/v1/index/search": {"get": {"summary": "Code index search", "parameters":[{"name":"q","in":"query","required":true,"schema":{"type":"string"}}]}},
    "/api/v1/index/rebuild": {"post": {"summary": "Rebuild code index"}},
    "/api/v1/index/stats": {"get": {"summary": "Code index stats"}},
    "/api/v1/device/code": {"post": {"summary": "RFC8628 device authorization (issue device_code + user_code)", "security": [], "responses": {"200": {"description": "deviceCode,userCode,verificationUri,expiresIn,interval"}}}},
    "/api/v1/device/token": {"post": {"summary": "RFC8628 device token polling (grant_type=device_code)", "security": [], "responses": {"200": {"description": "tokens once approved"}, "400": {"description": "authorization_pending / slow_down / access_denied / expired_token / invalid_grant"}}}},
    "/api/v1/device/approve": {"post": {"summary": "Approve/deny a device by user_code (requires Bearer JWT)", "responses": {"200": {"description": "ok"}}}},
    "/api/v1/admin/log-level": {"get": {"summary": "Get log level"}, "post": {"summary": "Set log level"}},
    "/api/v1/openapi.json": {"get": {"summary": "This document", "security": []}},
    "/metrics": {"get": {"summary": "Prometheus text", "security": []}},
    "/docs": {"get": {"summary": "Swagger UI", "security": []}},
    "/ws/host": {"get": {"summary": "Host-agent WebSocket", "parameters":[
      {"name":"token","in":"query","schema":{"type":"string"}},
      {"name":"deviceId","in":"query","schema":{"type":"string"}}]}}
  }
}`
