package repository

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/tenant"
)

// ErrTenantMissing is returned by ListForUser implementations when ctx does
// not carry a tenant.Tenant (Sprint 1.6 invariant: business code must not
// query multi-tenant repositories outside an authenticated request).
var ErrTenantMissing = errors.New("tenant missing from context")

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
		// Multi-tenant isolation (Sprint 1.7): always filter by userID; sessionID is optional.
		if e.UserID != userID {
			continue
		}
		if sessionID != "" && e.SessionID != sessionID {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ListForUser is the ctx-driven (Sprint 1.6) form. Returns ErrTenantMissing if
// ctx has no tenant.Tenant — refusing to leak rows to a missing/unauthenticated
// caller is the safe default.
func (r *MemoryAuditRepo) ListForUser(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	t, ok := tenant.From(ctx)
	if !ok || t.UserID == "" {
		return nil, ErrTenantMissing
	}
	return r.ListBySession(ctx, t.UserID, sessionID, limit)
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
	// Multi-tenant isolation (Sprint 1.7): always scope by user_id; session_id is an optional refinement.
	q := `SELECT user_id, session_id, action, tool, IFNULL(detail,''), decision FROM audit_log WHERE user_id=?`
	args := []any{userID}
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

// ListForUser is the ctx-driven (Sprint 1.6) form: extracts the tenant from
// ctx and delegates to ListBySession so the isolation rule lives in one place.
func (r *MySQLAuditRepo) ListForUser(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	t, ok := tenant.From(ctx)
	if !ok || t.UserID == "" {
		return nil, ErrTenantMissing
	}
	return r.ListBySession(ctx, t.UserID, sessionID, limit)
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
