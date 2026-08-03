package repository

import (
	"context"
	"database/sql"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type MySQLMemoryRepo struct{ db *sql.DB }

func NewMySQLMemoryRepo(db *sql.DB) *MySQLMemoryRepo { return &MySQLMemoryRepo{db: db} }

func (r *MySQLMemoryRepo) Save(ctx context.Context, item *memport.MemoryItem) error {
	res, err := r.db.ExecContext(ctx, `
INSERT INTO core_memory (user_id, project_id, scope, category, content, importance, source)
VALUES (?,?,?,?,?,?,?)`,
		item.UserID, item.ProjectID, string(item.Scope), item.Category, item.Content, item.Importance, item.Source)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	item.ID = id
	return nil
}

func (r *MySQLMemoryRepo) List(ctx context.Context, userID, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
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
	q += ` ORDER BY importance DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return r.scan(ctx, q, args...)
}

func (r *MySQLMemoryRepo) Search(ctx context.Context, userID, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	// LIKE fallback; importance as soft rank
	q := `SELECT id,user_id,project_id,scope,category,content,importance,source FROM core_memory
WHERE user_id=? AND (scope='user' OR (scope='project' AND (project_id=? OR project_id='' OR ?= '')))`
	args := []any{userID, projectID, projectID}
	if query != "" {
		q += ` AND (content LIKE ? OR category LIKE ?)`
		like := "%" + query + "%"
		args = append(args, like, like)
	}
	q += ` ORDER BY importance DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return r.scan(ctx, q, args...)
}

func (r *MySQLMemoryRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM core_memory WHERE id=?`, id)
	return err
}

func (r *MySQLMemoryRepo) scan(ctx context.Context, q string, args ...any) ([]memport.MemoryItem, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

