package repository

import (
	"context"
	"database/sql"
	"sync"
)

type MemorySummaryRepo struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMemorySummaryRepo() *MemorySummaryRepo {
	return &MemorySummaryRepo{data: map[string]string{}}
}

func (r *MemorySummaryRepo) Save(_ context.Context, sessionID, summary string, _ int) error {
	r.mu.Lock()
	r.data[sessionID] = summary
	r.mu.Unlock()
	return nil
}

func (r *MemorySummaryRepo) Get(_ context.Context, sessionID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.data[sessionID], nil
}

type MySQLSummaryRepo struct{ db *sql.DB }

func NewMySQLSummaryRepo(db *sql.DB) *MySQLSummaryRepo { return &MySQLSummaryRepo{db: db} }

func (r *MySQLSummaryRepo) Save(ctx context.Context, sessionID, summary string, tokenEst int) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO session_summary (session_id, summary, token_est, version)
VALUES (?,?,?,1)
ON DUPLICATE KEY UPDATE summary=VALUES(summary), token_est=VALUES(token_est), version=version+1`,
		sessionID, summary, tokenEst)
	return err
}

func (r *MySQLSummaryRepo) Get(ctx context.Context, sessionID string) (string, error) {
	var s string
	err := r.db.QueryRowContext(ctx, `SELECT summary FROM session_summary WHERE session_id=?`, sessionID).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s, err
}
