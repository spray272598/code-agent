// Package kms provides concrete CryptoSealer implementations. Today it ships
// an AES-256-GCM sealer (Sprint 2.8) backed by a local keyfile with env var
// override. Future backends (GCP KMS / AWS KMS / Vault) live alongside.
package kms

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/kms"
)

const (
	algorithm = "aes-256-gcm"
	keyBytes  = 32 // AES-256
	keyFile   = "kms.key"
	envKey    = "CODE_AGENT_KMS_KEY"
)

// EnvDir is the directory used when KEY_PATH is unset (./secrets/kms.key).
// It's a var so tests can swap it.
var EnvDir = func() string { return "./secrets" }

// KeyFile is the file path used when env var CODE_AGENT_KMS_KEY is unset.
// Computed lazily from EnvDir() so tests can swap the dir.
func KeyFile() string { return filepath.Join(EnvDir(), keyFile) }

// Sealer is the AES-256-GCM implementation. Active is the current key;
// Previous is kept for decryption during rotation. Both are loaded by NewSealer.
type Sealer struct {
	mu       sync.RWMutex
	active   keySlot
	previous map[string]keySlot // keyID -> key
	alg      string
}

// keySlot bundles a key with its id for the rotation map.
type keySlot struct {
	id  string
	key [keyBytes]byte
}

// NewSealer loads (or initializes) the KMS key from one of:
//  1. the CODE_AGENT_KMS_KEY env var (hex, 64 chars)
//  2. the keyfile (./secrets/kms.key)
//
// A missing keyfile is auto-created with a freshly generated key.
//
// A second env var CODE_AGENT_KMS_PREVIOUS may carry a previous hex key for
// rotation; its key id is derived from the first 8 hex chars.
func NewSealer() (*Sealer, error) {
	active, err := loadOrCreateKey("primary")
	if err != nil {
		return nil, err
	}
	previous := map[string]keySlot{}
	if prev := os.Getenv("CODE_AGENT_KMS_PREVIOUS"); prev != "" {
		// Derive id the same way as a primary key so ciphertexts encrypted
		// under the primary can be decrypted by this slot.
		slot, err := parseKey("primary", prev)
		if err != nil {
			return nil, fmt.Errorf("KMS previous key: %w", err)
		}
		previous[slot.id] = slot
	}
	return &Sealer{active: active, previous: previous, alg: algorithm}, nil
}

func loadOrCreateKey(id string) (keySlot, error) {
	if h := os.Getenv(envKey); h != "" {
		return parseKey(id, h)
	}
	// load or create keyfile.
	path := KeyFile()
	if data, err := os.ReadFile(path); err == nil {
		s, err := parseKey(id, string(data))
		if err != nil {
			return keySlot{}, fmt.Errorf("parse keyfile %s: %w", path, err)
		}
		return s, nil
	}
	// generate fresh
	raw := make([]byte, keyBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return keySlot{}, fmt.Errorf("generate key: %w", err)
	}
	hex := fmt.Sprintf("%x", raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return keySlot{}, fmt.Errorf("mkdir %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(hex), 0o600); err != nil {
		return keySlot{}, fmt.Errorf("write keyfile %s: %w", path, err)
	}
	return parseKey(id, hex)
}

func parseKey(id, hexStr string) (keySlot, error) {
	if len(hexStr) != keyBytes*2 {
		return keySlot{}, fmt.Errorf("hex key must be %d chars, got %d", keyBytes*2, len(hexStr))
	}
	var k [keyBytes]byte
	_, err := fmt.Sscanf(hexStr, "%x", &k)
	if err != nil {
		// Sscanf on fixed-size array is finicky across platforms; do manual.
		if !decodeHex(k[:0], hexStr) {
			return keySlot{}, errors.New("invalid hex key")
		}
	}
	if id == "primary" {
		id = "k-" + shortID(hexStr)
	}
	return keySlot{id: id, key: k}, nil
}

func decodeHex(dst []byte, src string) bool {
	if len(src)%2 != 0 || len(dst) != 0 {
		return false
	}
	for i := 0; i < len(src); i += 2 {
		hi, ok := hexVal(src[i])
		if !ok {
			return false
		}
		lo, ok := hexVal(src[i+1])
		if !ok {
			return false
		}
		dst = append(dst, byte(hi)<<4|byte(lo))
	}
	return true
}

func hexVal(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

func shortID(hexStr string) string {
	if len(hexStr) >= 8 {
		return hexStr[:8]
	}
	return hexStr
}

// Encrypt seals plaintext with AES-256-GCM under the active key. The returned
// Ciphertext is opaque; callers pass it to Decrypt.
func (s *Sealer) Encrypt(_ context.Context, plaintext []byte) (kms.Ciphertext, error) {
	s.mu.RLock()
	key := s.active
	s.mu.RUnlock()
	block, err := aes.NewCipher(key.key[:])
	if err != nil {
		return kms.Ciphertext{}, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return kms.Ciphertext{}, fmt.Errorf("gcm new: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return kms.Ciphertext{}, fmt.Errorf("rand nonce: %w", err)
	}
	body := gcm.Seal(nil, nonce, plaintext, []byte(key.id))
	return kms.Ciphertext{
		KeyID:     key.id,
		Nonce:     nonce,
		Body:      body,
		Algorithm: s.alg,
		Version:   1,
	}, nil
}

// Decrypt opens a Ciphertext. Unknown KeyIDs return an error (fail closed);
// the previous key slot allows seamless rotation.
func (s *Sealer) Decrypt(_ context.Context, ct kms.Ciphertext) ([]byte, error) {
	if ct.Algorithm != algorithm {
		return nil, fmt.Errorf("unsupported algorithm %q", ct.Algorithm)
	}
	key, ok := s.lookup(ct.KeyID)
	if !ok {
		return nil, fmt.Errorf("unknown kms key id %q", ct.KeyID)
	}
	block, err := aes.NewCipher(key.key[:])
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm new: %w", err)
	}
	return gcm.Open(nil, ct.Nonce, ct.Body, []byte(ct.KeyID))
}

func (s *Sealer) lookup(id string) (keySlot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active.id == id {
		return s.active, true
	}
	k, ok := s.previous[id]
	return k, ok
}

// KeyID returns the active key id. Useful for logging + rotation auditing.
func (s *Sealer) KeyID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.id
}
