package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/spray272598/code-agent/internal/domain/session/model"
)

type MySQLSessionRepo struct{ db *sql.DB }

func NewMySQLSessionRepo(db *sql.DB) *MySQLSessionRepo { return &MySQLSessionRepo{db: db} }

func (r *MySQLSessionRepo) Save(ctx context.Context, s *model.Session) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO chat_session (id,user_id,project_id,agent_id,title,status,message_count,token_used,working_dir,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE title=VALUES(title),status=VALUES(status),message_count=VALUES(message_count),
 token_used=VALUES(token_used),working_dir=VALUES(working_dir),updated_at=VALUES(updated_at)`,
		s.ID, s.UserID, s.ProjectID, s.AgentID, s.Title, s.Status, s.MessageCount, s.TokenUsed, s.WorkingDir,
		s.CreatedAt, s.UpdatedAt)
	return err
}

func (r *MySQLSessionRepo) FindByID(ctx context.Context, id string) (*model.Session, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id,user_id,project_id,agent_id,title,status,message_count,token_used,working_dir,created_at,updated_at
FROM chat_session WHERE id=?`, id)
	var s model.Session
	err := row.Scan(&s.ID, &s.UserID, &s.ProjectID, &s.AgentID, &s.Title, &s.Status,
		&s.MessageCount, &s.TokenUsed, &s.WorkingDir, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *MySQLSessionRepo) ListByUser(ctx context.Context, userID string, limit int) ([]*model.Session, error) {
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
		var s model.Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.ProjectID, &s.AgentID, &s.Title, &s.Status,
			&s.MessageCount, &s.TokenUsed, &s.WorkingDir, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

type MySQLMessageRepo struct{ db *sql.DB }

func NewMySQLMessageRepo(db *sql.DB) *MySQLMessageRepo { return &MySQLMessageRepo{db: db} }

func (r *MySQLMessageRepo) Save(ctx context.Context, m *model.Message) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO chat_message (id,session_id,role,content,tool_name,tool_call_id,step,token_count,priority,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, m.ToolName, m.ToolCallID, m.Step, m.TokenCount, m.Priority, m.CreatedAt)
	return err
}

func (r *MySQLMessageRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]*model.Message, error) {
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
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolName, &m.ToolCallID,
			&m.Step, &m.TokenCount, &m.Priority, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (r *MySQLMessageRepo) ListAsMaps(ctx context.Context, sessionID string, limit int) ([]map[string]any, error) {
	list, err := r.ListBySession(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, map[string]any{
			"id": m.ID, "role": m.Role, "content": m.Content,
			"toolName": m.ToolName, "toolCallId": m.ToolCallID,
			"step": m.Step, "priority": m.Priority,
		})
	}
	return out, nil
}
