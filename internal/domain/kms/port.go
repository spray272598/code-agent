// Package kms is the Sprint 2.8 abstraction for the encryption layer that
// protects stored credentials (SSH passwords/private keys, LLM API keys, etc.).
//
// The interface is intentionally minimal: Encrypt/Decrypt round-trip a plaintext
// into a versioned ciphertext (the KeyID is embedded so we can rotate keys
// without re-encrypting everything). Implementations live in
// internal/infrastructure/kms.
package kms

import "context"

// CryptoSealer encrypts plaintexts for at-rest storage. The returned ciphertext
// is opaque; callers must not parse it. Implementations MUST thread ctx for
// cancellation, MUST embed a key id + nonce + tag, and MUST fail closed (return
// an error) when decryption fails — never silently return garbage.
//
// Concurrency: implementations are safe for concurrent use.
type CryptoSealer interface {
	Encrypt(ctx context.Context, plaintext []byte) (Ciphertext, error)
	Decrypt(ctx context.Context, ct Ciphertext) ([]byte, error)
	KeyID() string // the active key id; useful for logging + rotation auditing
}

// Ciphertext is the opaque output of Encrypt. Versions + KeyID make rotation
// possible without data migration (old ciphertexts stay decryptable via the
// previous key id held by the implementation).
type Ciphertext struct {
	KeyID     string // which key was used
	Nonce     []byte // unique per encrypt
	Body      []byte // ciphertext + auth tag (GCM) or sealed box
	Algorithm string // e.g. "aes-256-gcm"
	Version   int    // KMS wire-format version (increment on breaking changes)
}
