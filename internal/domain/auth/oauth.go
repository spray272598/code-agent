package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// OAuth2 Authorization Code + PKCE support (3.4 SSO baseline). This package
// implements the protocol-core (PKCE, authorization-code issuance/redeem, token
// minting) independent of any specific IdP; an HTTP adapter that talks to
// Google/GitHub/etc. can be layered on top without touching this logic.

const (
	ChallengeMethodS256  = "S256"
	ChallengeMethodPlain = "plain"
	// CodeVerifierMinLen/MaxLen per RFC 7636.
	CodeVerifierMinLen = 43
	CodeVerifierMaxLen = 128
)

// GenerateCodeVerifier returns a RFC 7636 code_verifier (URL-safe, 43–128 chars).
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CodeChallenge derives the code_challenge for a verifier using the given method.
func CodeChallenge(verifier, method string) (string, error) {
	switch method {
	case ChallengeMethodS256:
		sum := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(sum[:]), nil
	case ChallengeMethodPlain:
		return verifier, nil
	default:
		return "", fmt.Errorf("unsupported challenge method %q", method)
	}
}

// ValidatePKCE verifies a code_verifier against the stored challenge.
func ValidatePKCE(verifier, challenge, method string) bool {
	if len(verifier) < CodeVerifierMinLen || len(verifier) > CodeVerifierMaxLen {
		return false
	}
	if strings.ContainsAny(verifier, "\x00") {
		return false
	}
	got, err := CodeChallenge(verifier, method)
	if err != nil {
		return false
	}
	return got == challenge
}

// AuthCodeRecord is a pending authorization code bound to a user + PKCE context.
type AuthCodeRecord struct {
	Code          string
	UserID        string
	Email         string
	Role          string
	RedirectURI   string
	Challenge     string
	ChallengeMeth string
	ExpiresAt     time.Time
	Consumed      bool
}

// AuthCodeStore issues and redeems one-time authorization codes.
type AuthCodeStore interface {
	Issue(rec AuthCodeRecord) error
	// Redeem returns the record and marks it consumed; errors on missing,
	// expired, or already-consumed codes.
	Redeem(code string) (*AuthCodeRecord, error)
}

// MemoryAuthCodeStore is an in-memory, single-node AuthCodeStore (sufficient
// for M3; a Redis-backed store is a drop-in replacement for multi-node).
type MemoryAuthCodeStore struct {
	m  map[string]*AuthCodeRecord
	mu sync.Mutex
}

// NewMemoryAuthCodeStore builds an empty store.
func NewMemoryAuthCodeStore() *MemoryAuthCodeStore {
	return &MemoryAuthCodeStore{m: map[string]*AuthCodeRecord{}}
}

// Issue stores a code (overwriting any prior code for the same string).
func (s *MemoryAuthCodeStore) Issue(rec AuthCodeRecord) error {
	if rec.Code == "" {
		return errors.New("empty code")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[rec.Code] = &rec
	return nil
}

// Redeem atomically fetches + consumes a code.
func (s *MemoryAuthCodeStore) Redeem(code string) (*AuthCodeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.m[code]
	if !ok {
		return nil, errors.New("invalid authorization code")
	}
	if rec.Consumed {
		return nil, errors.New("authorization code already used")
	}
	if time.Now().After(rec.ExpiresAt) {
		delete(s.m, code)
		return nil, errors.New("authorization code expired")
	}
	rec.Consumed = true
	return rec, nil
}

// OAuthToken is the token response from a successful code exchange.
type OAuthToken struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	Scope        string
}

// ExchangeRequest carries the parameters of a token endpoint call.
type ExchangeRequest struct {
	Code         string
	ClientID     string
	RedirectURI  string
	CodeVerifier string
}

// ExchangeRedeem redeems an authorization code and mints tokens. The PKCE
// verifier must match the challenge bound at issuance. Tokens are signed with
// the provided HS256 secret (same as the rest of the platform).
func ExchangeRedeem(store AuthCodeStore, req ExchangeRequest, secret []byte, accessTTL, refreshTTL time.Duration) (*OAuthToken, error) {
	rec, err := store.Redeem(req.Code)
	if err != nil {
		return nil, err
	}
	if rec.RedirectURI != req.RedirectURI {
		return nil, errors.New("redirect_uri mismatch")
	}
	if !ValidatePKCE(req.CodeVerifier, rec.Challenge, rec.ChallengeMeth) {
		return nil, errors.New("PKCE verification failed")
	}
	now := time.Now()
	access, err := Sign(Claims{
		Sub:   rec.UserID,
		Email: rec.Email,
		Role:  rec.Role,
		Scope: "openid profile",
		Iat:   now.Unix(),
		Exp:   now.Add(accessTTL).Unix(),
	}, secret)
	if err != nil {
		return nil, err
	}
	refresh, err := Sign(Claims{
		Sub:   rec.UserID,
		Email: rec.Email,
		Role:  rec.Role,
		JTI:   NewULID(),
		Iat:   now.Unix(),
		Exp:   now.Add(refreshTTL).Unix(),
	}, secret)
	if err != nil {
		return nil, err
	}
	return &OAuthToken{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresIn:    int(accessTTL.Seconds()),
		Scope:        "openid profile",
	}, nil
}
