package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/spray272598/code-agent/internal/domain/audit"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/session/model"
)

// --- Session ---

type SQLiteSessionRepo struct{ db *sql.DB }

func NewSQLiteSessionRepo(db *sql.DB) *SQLiteSessionRepo { return &SQLiteSessionRepo{db: db} }

func (r *SQLiteSessionRepo) Save(ctx context.Context, s *model.Session) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO chat_session (id,user_id,project_id,agent_id,title,status,message_count,token_used,working_dir,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
 title=excluded.title, status=excluded.status, message_count=excluded.message_count,
 token_used=excluded.token_used, working_dir=excluded.working_dir, updated_at=excluded.updated_at`,
		s.ID, s.UserID, s.ProjectID, s.AgentID, s.Title, s.Status, s.MessageCount, s.TokenUsed, s.WorkingDir,
		s.CreatedAt.Format(time.RFC3339Nano), s.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteSessionRepo) FindByID(ctx context.Context, id string) (*model.Session, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id,user_id,project_id,agent_id,title,status,message_count,token_used,working_dir,created_at,updated_at
FROM chat_session WHERE id=?`, id)
	return scanSession(row)
}

func (r *SQLiteSessionRepo) ListByUser(ctx context.Context, userID string, limit int) ([]*model.Session, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id,user_id,project_id,agent_id,title,status,message_count,token_used,working_dir,created_at,updated_at
FROM chat_session WHERE user_id=? ORDER BY updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Session
	for rows.Next() {
		s, err := scanSessionRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanSession(row *sql.Row) (*model.Session, error) {
	var s model.Session
	var cAt, uAt string
	err := row.Scan(&s.ID, &s.UserID, &s.ProjectID, &s.AgentID, &s.Title, &s.Status,
		&s.MessageCount, &s.TokenUsed, &s.WorkingDir, &cAt, &uAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, cAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, uAt)
	return &s, nil
}

func scanSessionRows(rows *sql.Rows) (*model.Session, error) {
	var s model.Session
	var cAt, uAt string
	err := rows.Scan(&s.ID, &s.UserID, &s.ProjectID, &s.AgentID, &s.Title, &s.Status,
		&s.MessageCount, &s.TokenUsed, &s.WorkingDir, &cAt, &uAt)
	if err != nil {
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, cAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, uAt)
	return &s, nil
}

// --- Message ---

type SQLiteMessageRepo struct{ db *sql.DB }

func NewSQLiteMessageRepo(db *sql.DB) *SQLiteMessageRepo { return &SQLiteMessageRepo{db: db} }

func (r *SQLiteMessageRepo) Save(ctx context.Context, m *model.Message) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO chat_message (id,session_id,role,content,tool_name,tool_call_id,step,token_count,priority,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, m.ToolName, m.ToolCallID, m.Step, m.TokenCount, m.Priority,
		m.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteMessageRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]*model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id,session_id,role,content,tool_name,tool_call_id,step,token_count,priority,created_at
FROM chat_message WHERE session_id=? ORDER BY created_at ASC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Message
	for rows.Next() {
		var m model.Message
		var cAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolName, &m.ToolCallID,
			&m.Step, &m.TokenCount, &m.Priority, &cAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, cAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (r *SQLiteMessageRepo) ListAsMaps(ctx context.Context, sessionID string, limit int) ([]map[string]any, error) {
	list, err := r.ListBySession(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	// keep only last `limit` if more? ListBySession already limited
	out := make([]map[string]any, 0, len(list))
	// if we got ascending full list but want last N — already limited
	start := 0
	if limit > 0 && len(list) > limit {
		start = len(list) - limit
	}
	for _, m := range list[start:] {
		out = append(out, map[string]any{
			"role": m.Role, "content": m.Content, "toolName": m.ToolName,
			"toolCallId": m.ToolCallID, "step": m.Step, "priority": m.Priority,
		})
	}
	return out, nil
}

// --- Summary ---

type SQLiteSummaryRepo struct{ db *sql.DB }

func NewSQLiteSummaryRepo(db *sql.DB) *SQLiteSummaryRepo { return &SQLiteSummaryRepo{db: db} }

func (r *SQLiteSummaryRepo) Save(ctx context.Context, sessionID, summary string, tokenEst int) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO session_summary (session_id,summary,token_est,updated_at) VALUES (?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET summary=excluded.summary, token_est=excluded.token_est, updated_at=excluded.updated_at`,
		sessionID, summary, tokenEst, time.Now().Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteSummaryRepo) Get(ctx context.Context, sessionID string) (string, error) {
	var s string
	err := r.db.QueryRowContext(ctx, `SELECT summary FROM session_summary WHERE session_id=?`, sessionID).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s, err
}

// --- Memory ---

type SQLiteMemoryRepo struct{ db *sql.DB }

func NewSQLiteMemoryRepo(db *sql.DB) *SQLiteMemoryRepo { return &SQLiteMemoryRepo{db: db} }

func (r *SQLiteMemoryRepo) Save(ctx context.Context, item *memport.MemoryItem) error {
	if item.ID == 0 {
		res, err := r.db.ExecContext(ctx, `
INSERT INTO core_memory (user_id,project_id,scope,category,content,importance,source,created_at)
VALUES (?,?,?,?,?,?,?,?)`,
			item.UserID, item.ProjectID, string(item.Scope), item.Category, item.Content,
			item.Importance, item.Source, time.Now().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		item.ID = id
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE core_memory SET content=?, importance=?, category=?, scope=? WHERE id=?`,
		item.Content, item.Importance, item.Category, string(item.Scope), item.ID)
	return err
}

func (r *SQLiteMemoryRepo) List(ctx context.Context, userID, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT id,user_id,project_id,scope,category,content,importance,source FROM core_memory WHERE user_id=?`
	args := []any{userID}
	if scope != "" {
		q += ` AND scope=?`
		args = append(args, string(scope))
	}
	if scope == memport.ScopeProject && projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	q += ` ORDER BY importance DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemRows(rows)
}

func (r *SQLiteMemoryRepo) Search(ctx context.Context, userID, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	// FTS-lite: LIKE over content; score by importance
	rows, err := r.db.QueryContext(ctx, `
SELECT id,user_id,project_id,scope,category,content,importance,source FROM core_memory
WHERE user_id=? AND content LIKE ?
ORDER BY importance DESC LIMIT ?`, userID, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanMemRows(rows)
	if err != nil {
		return nil, err
	}
	// filter project scope if needed
	if projectID == "" {
		return items, nil
	}
	var out []memport.MemoryItem
	for _, it := range items {
		if it.Scope != memport.ScopeProject || it.ProjectID == projectID || it.ProjectID == "" {
			out = append(out, it)
		}
	}
	return out, nil
}

func (r *SQLiteMemoryRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM core_memory WHERE id=?`, id)
	return err
}

func scanMemRows(rows *sql.Rows) ([]memport.MemoryItem, error) {
	var out []memport.MemoryItem
	for rows.Next() {
		var it memport.MemoryItem
		var scope string
		if err := rows.Scan(&it.ID, &it.UserID, &it.ProjectID, &scope, &it.Category, &it.Content, &it.Importance, &it.Source); err != nil {
			return nil, err
		}
		it.Scope = memport.Scope(scope)
		out = append(out, it)
	}
	return out, rows.Err()
}

// --- Audit ---

type SQLiteAuditRepo struct{ db *sql.DB }

func NewSQLiteAuditRepo(db *sql.DB) *SQLiteAuditRepo { return &SQLiteAuditRepo{db: db} }

func (r *SQLiteAuditRepo) Append(ctx context.Context, e audit.Entry) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO audit_log (user_id,session_id,action,tool,detail,decision,latency_ms,created_at)
VALUES (?,?,?,?,?,?,?,?)`,
		e.UserID, e.SessionID, e.Action, e.Tool, e.Detail, e.Decision, e.LatencyMs,
		time.Now().Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteAuditRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT user_id,session_id,action,tool,detail,decision,latency_ms,created_at FROM audit_log`
	var args []any
	if sessionID != "" {
		q += ` WHERE session_id=?`
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
		var cAt string
		if err := rows.Scan(&e.UserID, &e.SessionID, &e.Action, &e.Tool, &e.Detail, &e.Decision, &e.LatencyMs, &cAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
