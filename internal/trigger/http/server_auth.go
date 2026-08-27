package http

// Auth handlers (Sprint 1.2): signup, email verification, login, token refresh,
// user profile (me/updateProfile/changePassword), and password reset flow.
// Also includes authJWT middleware that validates Bearer access tokens.

import (
	"net/http"
	"time"

	authdomain "github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/observability"
	"go.opentelemetry.io/otel/trace"
)

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
