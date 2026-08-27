package http

// Device authorization handlers (RFC8628, Sprint 1.4): device code issuance,
// token polling, and user approval/denial.

import (
	"fmt"
	"net/http"

	"github.com/spray272598/code-agent/internal/application"
	authdomain "github.com/spray272598/code-agent/internal/domain/auth"
)

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
