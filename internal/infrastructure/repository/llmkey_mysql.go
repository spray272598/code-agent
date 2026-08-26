package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
)

// MySQLLLMKeyRepo is the Sprint 2.3 MySQL-backed implementation.
type MySQLLLMKeyRepo struct {
	db     *sql.DB
	sealer kms.CryptoSealer
}

func NewMySQLLLMKeyRepo(db *sql.DB, sealer kms.CryptoSealer) *MySQLLLMKeyRepo {
	return &MySQLLLMKeyRepo{db: db, sealer: sealer}
}

func (r *MySQLLLMKeyRepo) Save(ctx context.Context, k llmkey.Key) error {
	if k.UserID == "" {
		return llmkey.ErrTenantMissing
	}
	apiKeyCT, err := encryptStored(ctx, r.sealer, k.APIKey)
	if err != nil {
		return err
	}
	var apiBaseCT string
	if k.APIBase != "" {
		apiBaseCT, err = encryptStored(ctx, r.sealer, k.APIBase)
		if err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err = r.db.ExecContext(ctx, `
INSERT INTO llm_keys (user_id, alias, provider, api_key_kms, api_base_kms, enabled, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE provider=VALUES(provider), api_key_kms=VALUES(api_key_kms),
  api_base_kms=VALUES(api_base_kms), enabled=VALUES(enabled), updated_at=VALUES(updated_at)`,
		k.UserID, k.Alias, k.Provider, apiKeyCT, apiBaseCT, k.Enabled, now, now)
	return err
}

func (r *MySQLLLMKeyRepo) Get(ctx context.Context, userID, alias string) (*llmkey.Key, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT provider, api_key_kms, api_base_kms, enabled FROM llm_keys WHERE user_id=? AND alias=?`, userID, alias)
	return r.scan(ctx, row, userID, alias)
}

func (r *MySQLLLMKeyRepo) ListByUser(ctx context.Context, userID string) ([]llmkey.Key, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT alias, provider, api_key_kms, api_base_kms, enabled FROM llm_keys WHERE user_id=? ORDER BY alias`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]llmkey.Key, 0)
	for rows.Next() {
		var alias, provider, apiKeyCT, apiBaseCT string
		var enabled bool
		if err := rows.Scan(&alias, &provider, &apiKeyCT, &apiBaseCT, &enabled); err != nil {
			return nil, err
		}
		apiKey, err := decryptStored(ctx, r.sealer, apiKeyCT)
		if err != nil {
			return nil, err
		}
		apiBase, err := decryptStored(ctx, r.sealer, apiBaseCT)
		if err != nil {
			return nil, err
		}
		out = append(out, llmkey.Key{
			UserID:   userID,
			Alias:    alias,
			Provider: provider,
			APIKey:   apiKey,
			APIBase:  apiBase,
			Enabled:  enabled,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *MySQLLLMKeyRepo) Delete(ctx context.Context, userID, alias string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM llm_keys WHERE user_id=? AND alias=?`, userID, alias)
	return err
}

func (r *MySQLLLMKeyRepo) scan(ctx context.Context, row *sql.Row, userID, alias string) (*llmkey.Key, error) {
	var provider, apiKeyCT, apiBaseCT string
	var enabled bool
	err := row.Scan(&provider, &apiKeyCT, &apiBaseCT, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, llmkey.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	apiKey, err := decryptStored(ctx, r.sealer, apiKeyCT)
	if err != nil {
		return nil, err
	}
	apiBase, err := decryptStored(ctx, r.sealer, apiBaseCT)
	if err != nil {
		return nil, err
	}
	return &llmkey.Key{
		UserID:   userID,
		Alias:    alias,
		Provider: provider,
		APIKey:   apiKey,
		APIBase:  apiBase,
		Enabled:  enabled,
	}, nil
}
