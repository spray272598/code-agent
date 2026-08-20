package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
)

// SQLiteLLMKeyRepo is the Sprint 2.3 SQLite-backed implementation.
type SQLiteLLMKeyRepo struct {
	db     *sql.DB
	sealer kms.CryptoSealer
}

func NewSQLiteLLMKeyRepo(db *sql.DB, sealer kms.CryptoSealer) *SQLiteLLMKeyRepo {
	return &SQLiteLLMKeyRepo{db: db, sealer: sealer}
}

func (r *SQLiteLLMKeyRepo) Save(ctx context.Context, k llmkey.Key) error {
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
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
INSERT INTO llm_keys (user_id, alias, provider, api_key_kms, api_base_kms, enabled, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT (user_id, alias) DO UPDATE SET
  provider=excluded.provider,
  api_key_kms=excluded.api_key_kms,
  api_base_kms=excluded.api_base_kms,
  enabled=excluded.enabled,
  updated_at=excluded.updated_at`,
		k.UserID, k.Alias, k.Provider, apiKeyCT, apiBaseCT, boolToInt(k.Enabled), now, now)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("sqlite llm_keys save: no rows")
	}
	return nil
}

func (r *SQLiteLLMKeyRepo) Get(ctx context.Context, userID, alias string) (*llmkey.Key, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT provider, api_key_kms, api_base_kms, enabled FROM llm_keys WHERE user_id=? AND alias=?`, userID, alias)
	return r.scan(ctx, row, userID, alias)
}

func (r *SQLiteLLMKeyRepo) ListByUser(ctx context.Context, userID string) ([]llmkey.Key, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT alias, provider, api_key_kms, api_base_kms, enabled FROM llm_keys WHERE user_id=? ORDER BY alias`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]llmkey.Key, 0)
	for rows.Next() {
		var alias, provider, apiKeyCT, apiBaseCT string
		var enabled int
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
			Enabled:  enabled == 1,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *SQLiteLLMKeyRepo) Delete(ctx context.Context, userID, alias string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM llm_keys WHERE user_id=? AND alias=?`, userID, alias)
	return err
}

func (r *SQLiteLLMKeyRepo) scan(ctx context.Context, row *sql.Row, userID, alias string) (*llmkey.Key, error) {
	var provider, apiKeyCT, apiBaseCT string
	var enabled int
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
		Enabled:  enabled == 1,
	}, nil
}