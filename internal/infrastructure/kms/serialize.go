package kms

import (
	"encoding/base64"
	"fmt"

	"github.com/spray272598/code-agent/internal/domain/kms"
)

// Encode serializes a Ciphertext to a single base64 string suitable for
// storage in a VARCHAR / TEXT column. Format: v1:<keyID>:<b64(nonce|body)>.
// Versioning is explicit so future formats can be detected.
func Encode(ct kms.Ciphertext) string {
	raw := make([]byte, 0, len(ct.Nonce)+len(ct.Body))
	raw = append(raw, ct.Nonce...)
	raw = append(raw, ct.Body...)
	return fmt.Sprintf("v%d:%s:%s", ct.Version, ct.KeyID, base64.StdEncoding.EncodeToString(raw))
}

// Decode parses the output of Encode. Malformed input returns an error
// (fail closed). Unsupported versions also fail.
func Decode(s string) (kms.Ciphertext, error) {
	if len(s) < 4 || s[0] != 'v' || s[1] != '1' || s[2] != ':' {
		return kms.Ciphertext{}, fmt.Errorf("kms: missing v1: prefix")
	}
	var version int
	if _, err := fmt.Sscanf(s, "v%d:", &version); err != nil {
		return kms.Ciphertext{}, fmt.Errorf("kms: bad version: %w", err)
	}
	switch version {
	case 1:
		// v1:<keyID>:<b64>
		rest := s[3:] // skip "v1:"
		colon := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == ':' {
				colon = i
				break
			}
		}
		if colon < 0 {
			return kms.Ciphertext{}, fmt.Errorf("kms: missing key id separator")
		}
		keyID := rest[:colon]
		payload := rest[colon+1:]
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return kms.Ciphertext{}, fmt.Errorf("kms: base64: %w", err)
		}
		// nonce size = 12 for AES-256-GCM; tag = 16; body = ciphertext+tag.
		if len(raw) < 28 {
			return kms.Ciphertext{}, fmt.Errorf("kms: payload too short")
		}
		return kms.Ciphertext{
			KeyID:     keyID,
			Nonce:     raw[:12],
			Body:      raw[12:],
			Algorithm: algorithm,
			Version:   version,
		}, nil
	default:
		return kms.Ciphertext{}, fmt.Errorf("kms: unsupported version %d", version)
	}
}