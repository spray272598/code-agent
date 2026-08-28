// Package skillmarket provides remote skill marketplace client with signature verification.
// It implements the skill.Marketplace interface for fetching skills from a remote registry.
package skillmarket

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/skill"
)

// RemoteMarketplace fetches skill listings from a remote HTTP registry.
// It implements skill.Marketplace.
type RemoteMarketplace struct {
	baseURL    string
	publicKey  ed25519.PublicKey // nil = skip verification
	httpClient *http.Client
	cacheDir   string // optional local cache for downloaded listings
}

// Option configures the remote marketplace.
type Option func(*RemoteMarketplace)

// WithEd25519PublicKey enables signature verification with the given public key.
func WithEd25519PublicKey(pub ed25519.PublicKey) Option {
	return func(m *RemoteMarketplace) {
		m.publicKey = pub
	}
}

// WithCacheDir enables local caching of fetched listings.
func WithCacheDir(dir string) Option {
	return func(m *RemoteMarketplace) {
		m.cacheDir = dir
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(m *RemoteMarketplace) {
		m.httpClient = c
	}
}

// NewRemoteMarketplace creates a remote marketplace client.
func NewRemoteMarketplace(baseURL string, opts ...Option) *RemoteMarketplace {
	m := &RemoteMarketplace{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// --- skill.Marketplace interface ---

// ListingResponse is the API response for /api/v1/skills.
type ListingResponse struct {
	Listings []skill.SkillListing `json:"listings"`
}

// SkillDetailResponse is the API response for /api/v1/skills/:id.
type SkillDetailResponse struct {
	Listing  skill.SkillListing `json:"listing"`
	SkillMD  string             `json:"skill_md"`  // raw SKILL.md content
	Signature string            `json:"signature"` // ed25519 signature of skill_md (hex)
}

// List fetches all skill listings from the remote registry.
func (m *RemoteMarketplace) List(ctx context.Context) ([]skill.SkillListing, error) {
	url := m.baseURL + "/api/v1/skills"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		// Fallback to cache if available
		if cached, cacheErr := m.loadCache(); cacheErr == nil {
			slog.Default().Warn("remote marketplace unreachable, using cache",
				"url", url, "error", err, "cached_count", len(cached))
			return cached, nil
		}
		return nil, fmt.Errorf("fetch listings from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	var listingResp ListingResponse
	if err := json.NewDecoder(resp.Body).Decode(&listingResp); err != nil {
		return nil, fmt.Errorf("decode listings: %w", err)
	}

	// Cache the response
	_ = m.saveCache(listingResp.Listings)

	slog.Default().Info("fetched remote skill listings",
		"url", url,
		"count", len(listingResp.Listings),
	)

	return listingResp.Listings, nil
}

// SourceDir fetches a skill's content and writes it to a temp directory.
// Returns the temp directory path for use with Service.InstallFromPath.
func (m *RemoteMarketplace) SourceDir(id string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/v1/skills/%s", m.baseURL, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch skill %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d for skill %s", resp.StatusCode, id)
	}

	var detail SkillDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return "", fmt.Errorf("decode skill detail: %w", err)
	}

	// Verify signature if public key is configured
	if m.publicKey != nil && detail.Signature != "" {
		sigBytes, err := hex.DecodeString(detail.Signature)
		if err != nil {
			return "", fmt.Errorf("decode signature: %w", err)
		}
		if !ed25519.Verify(m.publicKey, []byte(detail.SkillMD), sigBytes) {
			return "", fmt.Errorf("signature verification failed for skill %s", id)
		}
		slog.Default().Info("skill signature verified", "id", id)
	}

	// Write to temp directory
	tmpDir, err := os.MkdirTemp("", "skill-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	skillDir := filepath.Join(tmpDir, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("create skill dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(detail.SkillMD), 0o644); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("write SKILL.md: %w", err)
	}

	slog.Default().Info("skill fetched and verified",
		"id", id,
		"dir", skillDir,
		"verified", m.publicKey != nil && detail.Signature != "",
	)

	return tmpDir, nil
}

// --- Local cache helpers ---

func (m *RemoteMarketplace) cachePath() string {
	if m.cacheDir == "" {
		return ""
	}
	return filepath.Join(m.cacheDir, "listings.json")
}

func (m *RemoteMarketplace) loadCache() ([]skill.SkillListing, error) {
	p := m.cachePath()
	if p == "" {
		return nil, fmt.Errorf("no cache configured")
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}

	var listings []skill.SkillListing
	if err := json.Unmarshal(data, &listings); err != nil {
		return nil, err
	}
	return listings, nil
}

func (m *RemoteMarketplace) saveCache(listings []skill.SkillListing) error {
	if m.cacheDir == "" {
		return nil
	}

	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(listings)
	if err != nil {
		return err
	}

	return os.WriteFile(m.cachePath(), data, 0o644)
}

// --- Signature helpers ---

// LoadEd25519PublicKey loads an Ed25519 public key from a PEM or raw hex file.
func LoadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}

	// Try hex encoding first (32 bytes = 64 hex chars)
	hexStr := strings.TrimSpace(string(data))
	if hexStr != "" {
		keyBytes, err := hex.DecodeString(hexStr)
		if err == nil && len(keyBytes) == ed25519.PublicKeySize {
			return ed25519.PublicKey(keyBytes), nil
		}
	}

	return nil, fmt.Errorf("invalid Ed25519 public key format (expected 32-byte hex)")
}

// LoadEd25519PublicKeyFromEnv loads the public key from SKILL_REGISTRY_PUBKEY env var.
func LoadEd25519PublicKeyFromEnv() ed25519.PublicKey {
	hexKey := os.Getenv("SKILL_REGISTRY_PUBKEY")
	if hexKey == "" {
		return nil
	}
	keyBytes, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(keyBytes) != ed25519.PublicKeySize {
		slog.Default().Warn("invalid SKILL_REGISTRY_PUBKEY, signature verification disabled")
		return nil
	}
	return ed25519.PublicKey(keyBytes)
}

// GenerateKeyPair generates an Ed25519 key pair for testing.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(nil)
}

// SignSkill signs SKILL.md content with the given private key (hex output).
func SignSkill(skillMD string, priv ed25519.PrivateKey) string {
	sig := ed25519.Sign(priv, []byte(skillMD))
	return hex.EncodeToString(sig)
}
