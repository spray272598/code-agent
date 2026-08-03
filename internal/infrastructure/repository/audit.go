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

func (r *MemoryAuditRepo) ListBySession(_ context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var out []audit.Entry
	for i := len(r.data) - 1; i >= 0 && len(out) < limit; i-- {
		if sessionID == "" || r.data[i].SessionID == sessionID {
			out = append(out, r.data[i])
		}
	}
	return out, nil
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

func (r *MySQLAuditRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT user_id, session_id, action, tool, IFNULL(detail,''), decision FROM audit_log
WHERE session_id=? ORDER BY id DESC LIMIT ?`, sessionID, limit)
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
