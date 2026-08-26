package security

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type JWTManager struct {
	mu     sync.RWMutex
	tokens map[string]*TokenInfo
}

type TokenInfo struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	UserID    string    `json:"userId"`
	TeamID    string    `json:"teamId,omitempty"`
	OrgID     string    `json:"orgId,omitempty"`
	Issuer    string    `json:"issuer,omitempty"`
}

func NewJWTManager() *JWTManager {
	return &JWTManager{
		tokens: make(map[string]*TokenInfo),
	}
}

func (m *JWTManager) RegisterToken(key string, token string) error {
	exp, err := m.ParseExpiration(token)
	if err != nil {
		return err
	}

	info := &TokenInfo{
		Token:     token,
		ExpiresAt: exp,
	}

	claims, err := parseJWTClaims(token)
	if err == nil {
		if sub, ok := claims["sub"].(string); ok {
			info.UserID = sub
		}
		if tid, ok := claims["team_id"].(string); ok {
			info.TeamID = tid
		}
		if oid, ok := claims["org_id"].(string); ok {
			info.OrgID = oid
		}
		if iss, ok := claims["iss"].(string); ok {
			info.Issuer = iss
		}
	}

	m.mu.Lock()
	m.tokens[key] = info
	m.mu.Unlock()
	return nil
}

func (m *JWTManager) ParseExpiration(token string) (time.Time, error) {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return time.Time{}, err
	}
	expRaw, ok := claims["exp"]
	if !ok {
		return time.Time{}, fmt.Errorf("no expiration claim in JWT")
	}
	var expUnix int64
	switch v := expRaw.(type) {
	case float64:
		expUnix = int64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid exp value: %w", err)
		}
		expUnix = n
	case string:
		if _, err := fmt.Sscanf(v, "%d", &expUnix); err != nil {
			return time.Time{}, fmt.Errorf("invalid exp string: %w", err)
		}
	default:
		return time.Time{}, fmt.Errorf("unsupported exp type: %T", expRaw)
	}
	return time.Unix(expUnix, 0), nil
}

func (m *JWTManager) IsExpired(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.tokens[key]
	if !ok {
		return true
	}
	return time.Now().After(info.ExpiresAt)
}

func (m *JWTManager) IsExpiredOrNear(key string, threshold time.Duration) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.tokens[key]
	if !ok {
		return true
	}
	remaining := time.Until(info.ExpiresAt)
	return remaining <= threshold
}

func (m *JWTManager) GetToken(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.tokens[key]
	if !ok {
		return "", false
	}
	if time.Now().After(info.ExpiresAt) {
		return "", false
	}
	return info.Token, true
}

func (m *JWTManager) GetTokenInfo(key string) (*TokenInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.tokens[key]
	if !ok {
		return nil, false
	}
	cp := *info
	return &cp, true
}

func (m *JWTManager) RefreshAfterUnauthorized(key string, refreshFn func(string) (string, error)) (string, error) {
	m.mu.RLock()
	info, ok := m.tokens[key]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("token key not found: %s", key)
	}

	newToken, err := refreshFn(info.Token)
	if err != nil {
		return "", fmt.Errorf("token refresh failed: %w", err)
	}

	if err := m.RegisterToken(key, newToken); err != nil {
		return "", err
	}

	return newToken, nil
}

func (m *JWTManager) RemoveToken(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, key)
}

func (m *JWTManager) CleanupExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	removed := 0
	for k, v := range m.tokens {
		if now.After(v.ExpiresAt) {
			delete(m.tokens, k)
			removed++
		}
	}
	return removed
}

func (m *JWTManager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tokens)
}

func (m *JWTManager) ListKeys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.tokens))
	for k := range m.tokens {
		keys = append(keys, k)
	}
	return keys
}

func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("JWT payload decode: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("JWT claims parse: %w", err)
	}
	return claims, nil
}

func IsJWTToken(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := base64.RawURLEncoding.DecodeString(p); err != nil {
			return false
		}
	}
	return true
}
