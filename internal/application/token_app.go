package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour
)

// TokenService issues and validates platform JWTs and manages refresh tokens.
// Access tokens are short-lived HS256 JWTs; refresh tokens are opaque values
// stored only as SHA-256 hashes (the raw value is returned to the client once).
type TokenService struct {
	users   auth.UserRepository
	refresh auth.RefreshTokenRepository
	secrets [][]byte
}

// NewTokenService wires the token engine. Empty secrets are dropped so they can
// never accidentally validate a token. At least one non-empty secret is required.
func NewTokenService(users auth.UserRepository, refresh auth.RefreshTokenRepository, secrets ...[]byte) *TokenService {
	var sec [][]byte
	for _, s := range secrets {
		if len(s) > 0 {
			sec = append(sec, s)
		}
	}
	if len(sec) == 0 {
		sec = append(sec, []byte("insecure-default-change-me"))
	}
	return &TokenService{users: users, refresh: refresh, secrets: sec}
}

// IssuePair mints a short-lived access token and a long-lived opaque refresh
// token bound to the user/org/device. The raw refresh token is returned once.
func (s *TokenService) IssuePair(ctx context.Context, u *auth.User, deviceID string) (access, refresh string, err error) {
	jid := auth.NewULID()
	now := time.Now()
	access, err = auth.Sign(auth.Claims{
		Sub:   u.ID,
		DID:   deviceID,
		Role:  u.Role,
		Email: u.Email,
		Scope: "agent",
		JTI:   jid,
		Iat:   now.Unix(),
		Exp:   now.Add(accessTokenTTL).Unix(),
	}, s.secrets[0])
	if err != nil {
		return "", "", err
	}
	rawRefresh := auth.RandomToken(48)
	rt := &auth.RefreshToken{
		ID:        jid,
		UserID:    u.ID,
		DeviceID:  deviceID,
		TokenHash: hashToken(rawRefresh),
		Scope:     "agent",
		ExpiresAt: now.Add(refreshTokenTTL),
		Revoked:   false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.refresh.Save(ctx, rt); err != nil {
		return "", "", err
	}
	return access, rawRefresh, nil
}

// Refresh validates an opaque refresh token, rotates it (revokes the old JTI),
// and returns a fresh token pair bound to the same user/device.
func (s *TokenService) Refresh(ctx context.Context, oldRefresh string) (access, refresh string, err error) {
	if oldRefresh == "" {
		return "", "", errors.New("refresh token required")
	}
	rt, err := s.refresh.FindByHash(ctx, hashToken(oldRefresh))
	if err != nil {
		return "", "", err
	}
	if rt == nil {
		return "", "", errors.New("invalid refresh token")
	}
	if rt.Revoked {
		return "", "", errors.New("refresh token revoked")
	}
	if time.Now().After(rt.ExpiresAt) {
		return "", "", errors.New("refresh token expired")
	}
	u, err := s.users.FindByID(ctx, rt.UserID)
	if err != nil {
		return "", "", err
	}
	if u == nil || u.Status != auth.StatusActive {
		return "", "", errors.New("account unavailable")
	}
	if err := s.refresh.Revoke(ctx, rt.ID); err != nil {
		return "", "", err
	}
	return s.IssuePair(ctx, u, rt.DeviceID)
}

// Validate checks an access token's signature and expiry against the known secrets.
func (s *TokenService) Validate(access string) (*auth.Claims, error) {
	return auth.Parse(access, s.secrets...)
}

// RevokeUser revokes every refresh token for a user (e.g. on logout / compromise).
func (s *TokenService) RevokeUser(ctx context.Context, userID string) error {
	return s.refresh.RevokeAllForUser(ctx, userID)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
