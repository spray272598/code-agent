package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
)

type OAuthConfig struct {
	Enabled          bool   `json:"enabled"`
	ClientID         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret,omitempty"`
	AuthURL          string `json:"authUrl"`
	TokenURL         string `json:"tokenUrl"`
	RedirectURI      string `json:"redirectUri"`
	Scopes           string `json:"scopes"`
	UsePKCE          bool   `json:"usePkce"`
	Provider         string `json:"provider"`
}

type OAuthToken struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Scope        string    `json:"scope,omitempty"`
}

type OAuthFlow struct {
	config      OAuthConfig
	tokenStore  *OAuthTokenStore
	httpClient  *http.Client
	mu          sync.Mutex
	pending     map[string]*pendingRequest
}

type pendingRequest struct {
	state        string
	codeVerifier string
	createdAt    time.Time
	expiresAt    time.Time
}

type OAuthTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]OAuthToken
}

func NewOAuthTokenStore() *OAuthTokenStore {
	return &OAuthTokenStore{tokens: make(map[string]OAuthToken)}
}

func (s *OAuthTokenStore) Save(serverName string, token OAuthToken) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[serverName] = token
}

func (s *OAuthTokenStore) Get(serverName string) (*OAuthToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokens[serverName]
	if !ok {
		return nil, errors.New("no token for server")
	}
	if time.Now().After(t.ExpiresAt) {
		return nil, errors.New("token expired")
	}
	return &t, nil
}

func (s *OAuthTokenStore) Remove(serverName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, serverName)
}

func NewOAuthFlow(config OAuthConfig, store *OAuthTokenStore) *OAuthFlow {
	return &OAuthFlow{
		config:     config,
		tokenStore: store,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		pending:    make(map[string]*pendingRequest),
	}
}

func (f *OAuthFlow) StartAuthFlow(ctx context.Context) (string, error) {
	if !f.config.Enabled {
		return "", errors.New("OAuth not enabled")
	}

	state := generateState()
	verifier, challenge := "", ""

	if f.config.UsePKCE {
		var err error
		verifier, err = auth.GenerateCodeVerifier()
		if err != nil {
			return "", fmt.Errorf("generate verifier: %w", err)
		}
		challenge, err = auth.CodeChallenge(verifier, auth.ChallengeMethodS256)
		if err != nil {
			return "", fmt.Errorf("code challenge: %w", err)
		}
	}

	f.mu.Lock()
	f.pending[state] = &pendingRequest{
		state:        state,
		codeVerifier: verifier,
		createdAt:    time.Now(),
		expiresAt:    time.Now().Add(15 * time.Minute),
	}
	f.mu.Unlock()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", f.config.ClientID)
	params.Set("redirect_uri", f.config.RedirectURI)
	params.Set("scope", f.config.Scopes)
	params.Set("state", state)

	if f.config.UsePKCE {
		params.Set("code_challenge", challenge)
		params.Set("code_challenge_method", "S256")
	}

	authURL := f.config.AuthURL
	if !strings.Contains(authURL, "?") {
		authURL += "?"
	} else {
		authURL += "&"
	}

	return authURL + params.Encode(), nil
}

func (f *OAuthFlow) HandleCallback(ctx context.Context, code, state string) (*OAuthToken, error) {
	f.mu.Lock()
	req, ok := f.pending[state]
	if !ok {
		f.mu.Unlock()
		return nil, errors.New("invalid state")
	}
	delete(f.pending, state)
	f.mu.Unlock()

	if time.Now().After(req.expiresAt) {
		return nil, errors.New("state expired")
	}

	return f.exchangeCode(ctx, code, req.codeVerifier)
}

func (f *OAuthFlow) exchangeCode(ctx context.Context, code, codeVerifier string) (*OAuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", f.config.RedirectURI)
	data.Set("client_id", f.config.ClientID)

	if f.config.ClientSecret != "" {
		data.Set("client_secret", f.config.ClientSecret)
	}

	if f.config.UsePKCE && codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", f.config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s - %s", resp.Status, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if tokenResp.ExpiresIn <= 0 || tokenResp.ExpiresIn > 86400 {
		expiresAt = time.Now().Add(1 * time.Hour)
	}

	return &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiresAt,
		Scope:        tokenResp.Scope,
	}, nil
}

func (f *OAuthFlow) RefreshToken(ctx context.Context, serverName string) (*OAuthToken, error) {
	token, err := f.tokenStore.Get(serverName)
	if err != nil {
		return nil, err
	}

	if token.RefreshToken == "" {
		return nil, errors.New("no refresh token available")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", token.RefreshToken)
	data.Set("client_id", f.config.ClientID)

	if f.config.ClientSecret != "" {
		data.Set("client_secret", f.config.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", f.config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed: %s - %s", resp.Status, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if tokenResp.ExpiresIn <= 0 || tokenResp.ExpiresIn > 86400 {
		expiresAt = time.Now().Add(1 * time.Hour)
	}

	newToken := &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiresAt,
		Scope:        tokenResp.Scope,
	}

	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken
	}

	f.tokenStore.Save(serverName, *newToken)
	return newToken, nil
}

func (f *OAuthFlow) GetAccessToken(ctx context.Context, serverName string) (string, error) {
	token, err := f.tokenStore.Get(serverName)
	if err != nil {
		return "", err
	}

	if time.Now().After(token.ExpiresAt.Add(-5 * time.Minute)) {
		newToken, err := f.RefreshToken(ctx, serverName)
		if err != nil {
			return "", fmt.Errorf("token refresh: %w", err)
		}
		return newToken.AccessToken, nil
	}

	return token.AccessToken, nil
}

func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func encodePKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
