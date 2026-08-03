package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// KeyStore holds SHA-256 hashes of API keys; verifies with constant-time compare.
type KeyStore struct {
	hashes [][]byte
}

// NewKeyStore hashes plaintext keys from config (never stores raw after construction).
func NewKeyStore(plainKeys []string) *KeyStore {
	var hashes [][]byte
	for _, k := range plainKeys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		// allow pre-hashed form: sha256:<hex>
		if strings.HasPrefix(k, "sha256:") {
			raw, err := hex.DecodeString(strings.TrimPrefix(k, "sha256:"))
			if err == nil && len(raw) == sha256.Size {
				hashes = append(hashes, raw)
				continue
			}
		}
		sum := sha256.Sum256([]byte(k))
		h := make([]byte, sha256.Size)
		copy(h, sum[:])
		hashes = append(hashes, h)
	}
	return &KeyStore{hashes: hashes}
}

// Empty means auth disabled (dev open) — caller decides policy.
func (s *KeyStore) Empty() bool {
	return s == nil || len(s.hashes) == 0
}

// Valid reports whether key matches any stored hash (constant-time per candidate).
func (s *KeyStore) Valid(key string) bool {
	if s.Empty() {
		return true
	}
	sum := sha256.Sum256([]byte(key))
	ok := 0
	for _, h := range s.hashes {
		// ConstantTimeCompare requires equal length
		if subtle.ConstantTimeCompare(sum[:], h) == 1 {
			ok = 1
		}
	}
	return ok == 1
}

// HashKey exports hex hash for admin tooling / config migration.
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return "sha256:" + hex.EncodeToString(sum[:])
}
