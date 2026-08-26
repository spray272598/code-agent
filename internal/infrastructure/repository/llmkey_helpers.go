package repository

import (
	"context"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/kms"
	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
)

// encryptStored seals plaintext with the KMS sealer and returns a column-safe
// "kms:v1:<base64>" string suitable for storing in a TEXT/VARCHAR column.
func encryptStored(ctx context.Context, sealer kms.CryptoSealer, plaintext string) (string, error) {
	ct, err := sealer.Encrypt(ctx, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return "kms:v1:" + kmsinfra.Encode(ct), nil
}

// decryptStored reverses encryptStored. Empty ciphertext is returned as empty
// plaintext (used for nullable fields like APIBase).
func decryptStored(ctx context.Context, sealer kms.CryptoSealer, ct string) (string, error) {
	if ct == "" {
		return "", nil
	}
	if !strings.HasPrefix(ct, "kms:v1:") {
		return "", nil // legacy plaintext
	}
	encoded := strings.TrimPrefix(ct, "kms:v1:")
	parsed, err := kmsinfra.Decode(encoded)
	if err != nil {
		return "", err
	}
	plain, err := sealer.Decrypt(ctx, parsed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
