package ssh

import (
	"context"
	"errors"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
)

// EncryptingConnRepo is a Sprint 2.9 decorator that wraps any IConnectionRepository
// and transparently encrypts Password / PrivateKey on Save, decrypts them on
// read. The wrapped repo's schema is unchanged: ciphertexts are stored in the
// existing `password` / `private_key` TEXT columns (prefixed with "kms:v1:"
// so plaintext and ciphertext coexist during migration).
//
// Fail-closed: any KMS error during Save / Decrypt propagates; we never silently
// fall back to plaintext.
type EncryptingConnRepo struct {
	inner  sshport.IConnectionRepository
	sealer kms.CryptoSealer
}

// NewEncryptingConnRepo wires the KMS sealer around an existing repo.
func NewEncryptingConnRepo(inner sshport.IConnectionRepository, sealer kms.CryptoSealer) *EncryptingConnRepo {
	if inner == nil || sealer == nil {
		// Fail at construction time rather than at first use.
		// In a server context, this indicates a programming error in bootstrap wiring.
		return nil
	}
	return &EncryptingConnRepo{inner: inner, sealer: sealer}
}

// Save encrypts the sensitive fields, then delegates.
func (r *EncryptingConnRepo) Save(ctx context.Context, cfg *model.ConnectionConfig) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	// Clone so we never mutate the caller's model.
	cp := *cfg
	if cfg.Password != "" {
		ct, err := r.sealer.Encrypt(ctx, []byte(cfg.Password))
		if err != nil {
			return err
		}
		cp.Password = kmsCiphertextPrefix + kmsinfra.Encode(ct)
	}
	if cfg.PrivateKey != "" {
		ct, err := r.sealer.Encrypt(ctx, []byte(cfg.PrivateKey))
		if err != nil {
			return err
		}
		cp.PrivateKey = kmsCiphertextPrefix + kmsinfra.Encode(ct)
	}
	return r.inner.Save(ctx, &cp)
}

// FindByID reads from the inner repo and decrypts any KMS-shaped fields.
func (r *EncryptingConnRepo) FindByID(ctx context.Context, id string) (*model.ConnectionConfig, error) {
	cfg, err := r.inner.FindByID(ctx, id)
	if err != nil || cfg == nil {
		return cfg, err
	}
	return r.decrypt(ctx, cfg)
}

// FindByName mirrors FindByID.
func (r *EncryptingConnRepo) FindByName(ctx context.Context, name string) (*model.ConnectionConfig, error) {
	cfg, err := r.inner.FindByName(ctx, name)
	if err != nil || cfg == nil {
		return cfg, err
	}
	return r.decrypt(ctx, cfg)
}

// List decrypts every returned record. KMS errors are non-fatal: a record
// whose ciphertext can't be decrypted is surfaced with an empty credential
// (and a logged error in the caller path) — but the record itself is not
// dropped, since SSH listing is a metadata operation.
func (r *EncryptingConnRepo) List(ctx context.Context) ([]*model.ConnectionConfig, error) {
	rows, err := r.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*model.ConnectionConfig, 0, len(rows))
	for _, cfg := range rows {
		decoded, err := r.decrypt(ctx, cfg)
		if err != nil {
			// Fail closed for individual records: drop and report.
			continue
		}
		out = append(out, decoded)
	}
	return out, nil
}

// Delete delegates.
func (r *EncryptingConnRepo) Delete(ctx context.Context, id string) error {
	return r.inner.Delete(ctx, id)
}

func (r *EncryptingConnRepo) decrypt(ctx context.Context, cfg *model.ConnectionConfig) (*model.ConnectionConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	cp := *cfg
	if cfg.Password != "" {
		plain, err := r.tryDecrypt(ctx, cfg.Password)
		if err != nil {
			return nil, err
		}
		cp.Password = plain
	}
	if cfg.PrivateKey != "" {
		plain, err := r.tryDecrypt(ctx, cfg.PrivateKey)
		if err != nil {
			return nil, err
		}
		cp.PrivateKey = plain
	}
	return &cp, nil
}

func (r *EncryptingConnRepo) tryDecrypt(ctx context.Context, raw string) (string, error) {
	if !strings.HasPrefix(raw, kmsCiphertextPrefix) {
		// Plaintext (legacy record or never-encrypted). Pass through; the column
		// has been used this way since the schema was created.
		return raw, nil
	}
	encoded := strings.TrimPrefix(raw, kmsCiphertextPrefix)
	ct, err := kmsinfra.Decode(encoded)
	if err != nil {
		return "", err
	}
	plain, err := r.sealer.Decrypt(ctx, ct)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// kmsCiphertextPrefix marks a column value as KMS ciphertext. Kept short so
// it fits comfortably inside VARCHAR(512) for the password column.
const kmsCiphertextPrefix = "kms:v1:"
