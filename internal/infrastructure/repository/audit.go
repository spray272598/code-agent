package repository

import (
	"context"
	"database/sql"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/audit"
)

// MemoryAuditRepo in-process audit ring buffer.
type MemoryAuditRepo struct {
	mu   sync.Mutex
	data []audit.Entry
	max  int
}

func NewMemoryAuditRepo() *MemoryAuditRepo {
	return &MemoryAuditRepo{data: make([]audit.Entry, 0, 256), max: 2000}
}

func (r *MemoryAuditRepo) Append(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, e)
	if len(r.data) > r.max {
		r.data = r.data[len(r.data)-r.max:]
	}
	return nil
}

func (r *MemoryAuditRepo) ListBySession(_ context.Context, userID, sessionID string, limit int) ([]audit.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var out []audit.Entry
	for i := len(r.data) - 1; i >= 0 && len(out) < limit; i-- {
		e := r.data[i]
		// Single-operator harness: an empty userID means "match any actor".
		if userID != "" && e.UserID != userID {
			continue
		}
		if sessionID != "" && e.SessionID != sessionID {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ListForUser is the ctx-driven form. The harness has no accounts, so it simply
// returns every actor's entries for the session (single-operator).
func (r *MemoryAuditRepo) ListForUser(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	return r.ListBySession(ctx, "", sessionID, limit)
}

// MySQLAuditRepo persists to audit_log.
type MySQLAuditRepo struct{ db *sql.DB }

func NewMySQLAuditRepo(db *sql.DB) *MySQLAuditRepo { return &MySQLAuditRepo{db: db} }

func (r *MySQLAuditRepo) Append(ctx context.Context, e audit.Entry) error {
	detail := e.Detail
	if e.LatencyMs > 0 {
		detail = detail + " latency_ms=" + itoa(e.LatencyMs)
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO audit_log (user_id, session_id, action, tool, detail, decision) VALUES (?,?,?,?,?,?)`,
		e.UserID, e.SessionID, e.Action, e.Tool, detail, e.Decision)
	return err
}

func (r *MySQLAuditRepo) ListBySession(ctx context.Context, userID, sessionID string, limit int) ([]audit.Entry, error) {
	if limit <= 0 {
		limit = 100
	}
	// Single-operator harness: an empty userID matches every actor; session_id is an optional refinement.
	q := `SELECT user_id, session_id, action, tool, IFNULL(detail,''), decision FROM audit_log WHERE 1=1`
	args := []any{}
	if userID != "" {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	if sessionID != "" {
		q += ` AND session_id=?`
		args = append(args, sessionID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []audit.Entry
	for rows.Next() {
		var e audit.Entry
		if err := rows.Scan(&e.UserID, &e.SessionID, &e.Action, &e.Tool, &e.Detail, &e.Decision); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListForUser is the ctx-driven form: the harness has no accounts, so it returns
// every actor's entries for the session (single-operator).
func (r *MySQLAuditRepo) ListForUser(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	return r.ListBySession(ctx, "", sessionID, limit)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
