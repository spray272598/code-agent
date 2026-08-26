package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
)

// Device authorization (RFC8628) service.
//
// Flow:
//  1. The device (e.g. the TUI) calls StartAuthorization and receives a
//     device_code (secret, shown only to the device) and a user_code (short,
//     shown to the user).
//  2. The user opens VerificationURI in a browser, authenticates, and submits
//     the user_code via Approve (or rejects via Deny).
//  3. The device polls Poll on a fixed interval; once approved it receives an
//     access/refresh token pair bound to the approving user.
type DeviceService struct {
	devices auth.DeviceRepository
	users   auth.UserRepository
	tokens  *TokenService

	verificationURI string
	codeTTL         time.Duration
	pollInterval    time.Duration
}

// DeviceAuthParams are the inputs to StartAuthorization.
type DeviceAuthParams struct {
	ClientID   string
	Scope      string
	DeviceName string
	Platform   string
	UserAgent  string
}

// DeviceAuthResult is returned to the device after it requests authorization.
type DeviceAuthResult struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationUri"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

func NewDeviceService(devices auth.DeviceRepository, users auth.UserRepository, tokens *TokenService, verificationURI string, codeTTL, pollInterval time.Duration) *DeviceService {
	if codeTTL <= 0 {
		codeTTL = 5 * time.Minute
	}
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if verificationURI == "" {
		verificationURI = "/activate"
	}
	return &DeviceService{
		devices:         devices,
		users:           users,
		tokens:          tokens,
		verificationURI: verificationURI,
		codeTTL:         codeTTL,
		pollInterval:    pollInterval,
	}
}

// StartAuthorization mints a fresh device_code (the primary key) and a
// human-friendly user_code, persists a pending device record, and returns the
// values the device should display to the user.
func (s *DeviceService) StartAuthorization(ctx context.Context, p DeviceAuthParams) (*DeviceAuthResult, error) {
	deviceCode := auth.RandomToken(48)
	userCode, err := s.mintUserCode(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	// Store the canonical (dash-free, upper) user_code; the formatted code is
	// only returned for display. Lookups via FindByUserCode are normalized too.
	d := &auth.Device{
		ID:        deviceCode,
		UserCode:  normalizeUserCode(userCode),
		Status:    auth.DevicePending,
		Scope:     p.Scope,
		ExpiresAt: now.Add(s.codeTTL),
		CreatedAt: now,
	}
	if err := s.devices.Save(ctx, d); err != nil {
		return nil, err
	}
	return &DeviceAuthResult{
		DeviceCode:      deviceCode,
		UserCode:        userCode,
		VerificationURI: s.verificationURI,
		ExpiresIn:       int(s.codeTTL.Seconds()),
		Interval:        int(s.pollInterval.Seconds()),
	}, nil
}

// PollStatus values for a device authorization request.
const (
	PollPending  = "pending"
	PollApproved = "approved"
	PollDenied   = "denied"
	PollExpired  = "expired"
	PollInvalid  = "invalid"
)

// PollOutcome is returned on every poll. For non-approved states OAuthError
// carries the RFC8628 error code; for approved states the token fields are
// populated. A non-nil error indicates an internal failure, never a protocol
// outcome.
type PollOutcome struct {
	Status       string
	OAuthError   string // authorization_pending | slow_down | access_denied | expired_token | invalid_grant
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
}

// Poll is called by the device on a fixed interval. It records the poll time
// (enforcing the interval via RFC8628 "slow_down") and, once the device has
// been approved, issues an access/refresh token pair bound to the approving
// user.
func (s *DeviceService) Poll(ctx context.Context, deviceCode string) (*PollOutcome, error) {
	if deviceCode == "" {
		return &PollOutcome{Status: PollInvalid, OAuthError: "invalid_grant"}, nil
	}
	d, err := s.devices.FindByDeviceCode(ctx, deviceCode)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return &PollOutcome{Status: PollInvalid, OAuthError: "invalid_grant"}, nil
	}
	now := time.Now()
	if now.After(d.ExpiresAt) {
		return &PollOutcome{Status: PollExpired, OAuthError: "expired_token"}, nil
	}
	if d.Status == auth.DeviceDenied {
		return &PollOutcome{Status: PollDenied, OAuthError: "access_denied"}, nil
	}
	// Ignore requests that arrive faster than the agreed polling interval.
	if !d.LastPollAt.IsZero() && now.Sub(d.LastPollAt) < s.pollInterval {
		return &PollOutcome{Status: PollPending, OAuthError: "slow_down"}, nil
	}
	// Touch the poll timestamp so the client must wait a full interval before
	// the next accepted poll. We persist the existing record as-is.
	d.LastPollAt = now
	if err := s.devices.Save(ctx, d); err != nil {
		return nil, err
	}
	if d.Status == auth.DeviceApproved {
		u, ferr := s.users.FindByID(ctx, d.UserID)
		if ferr != nil {
			return nil, ferr
		}
		if u == nil || u.Status != auth.StatusActive {
			return &PollOutcome{Status: PollDenied, OAuthError: "access_denied"}, nil
		}
		access, refresh, ierr := s.tokens.IssuePair(ctx, u, d.ID)
		if ierr != nil {
			return nil, ierr
		}
		return &PollOutcome{
			Status:       PollApproved,
			AccessToken:  access,
			RefreshToken: refresh,
			TokenType:    "Bearer",
			ExpiresIn:    int(accessTokenTTL.Seconds()),
		}, nil
	}
	return &PollOutcome{Status: PollPending, OAuthError: "authorization_pending"}, nil
}

// Approve binds an authenticated user (from the browser session) to the pending
// device identified by user_code.
func (s *DeviceService) Approve(ctx context.Context, userCode, userID string) error {
	userCode = normalizeUserCode(userCode)
	if userCode == "" {
		return ErrDeviceCodeRequired
	}
	if userID == "" {
		return ErrDeviceUserRequired
	}
	d, err := s.devices.FindByUserCode(ctx, userCode)
	if err != nil {
		return err
	}
	if d == nil {
		return ErrDeviceNotFound
	}
	if d.Status == auth.DeviceApproved {
		return ErrDeviceAlreadyApproved
	}
	if time.Now().After(d.ExpiresAt) {
		return ErrDeviceExpired
	}
	d.UserID = userID
	d.Status = auth.DeviceApproved
	d.ApprovedAt = time.Now()
	return s.devices.Save(ctx, d)
}

// Deny rejects a pending device authorization (e.g. the user clicks "Reject").
func (s *DeviceService) Deny(ctx context.Context, userCode string) error {
	userCode = normalizeUserCode(userCode)
	if userCode == "" {
		return ErrDeviceCodeRequired
	}
	d, err := s.devices.FindByUserCode(ctx, userCode)
	if err != nil {
		return err
	}
	if d == nil {
		return ErrDeviceNotFound
	}
	if d.Status == auth.DeviceApproved {
		return ErrDeviceAlreadyApproved
	}
	if time.Now().After(d.ExpiresAt) {
		return ErrDeviceExpired
	}
	d.Status = auth.DeviceDenied
	d.ApprovedAt = time.Now()
	return s.devices.Save(ctx, d)
}

// mintUserCode returns a unique (among non-expired devices) formatted user code.
func (s *DeviceService) mintUserCode(ctx context.Context) (string, error) {
	for i := 0; i < 5; i++ {
		display := formatUserCode(auth.RandomUserCode(8))
		canonical := normalizeUserCode(display)
		existing, err := s.devices.FindByUserCode(ctx, canonical)
		if err != nil {
			return "", err
		}
		if existing == nil || time.Now().After(existing.ExpiresAt) {
			return display, nil
		}
	}
	return "", ErrDeviceCodeUnavailable
}

// formatUserCode inserts a dash in the middle of the raw code so it reads as
// "ABCD-1234" for human entry.
func formatUserCode(raw string) string {
	if len(raw) < 2 {
		return raw
	}
	mid := len(raw) / 2
	return raw[:mid] + "-" + raw[mid:]
}

func normalizeUserCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(raw, "-", "")))
}

var (
	ErrDeviceNotFound        = errors.New("device authorization not found")
	ErrDeviceExpired         = errors.New("device authorization expired")
	ErrDeviceAlreadyApproved = errors.New("device authorization already approved")
	ErrDeviceCodeRequired    = errors.New("user_code required")
	ErrDeviceUserRequired    = errors.New("authenticated user required")
	ErrDeviceCodeUnavailable = errors.New("failed to generate a unique user code")
)
