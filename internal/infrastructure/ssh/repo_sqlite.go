package ssh

import (
	"context"
	"database/sql"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
)

type SQLiteConnRepo struct{ db *sql.DB }

func NewSQLiteConnRepo(db *sql.DB) *SQLiteConnRepo { return &SQLiteConnRepo{db: db} }

func (r *SQLiteConnRepo) Save(ctx context.Context, cfg *model.ConnectionConfig) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
INSERT INTO ssh_connection (id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,host=excluded.host,port=excluded.port,
 username=excluded.username,auth_type=excluded.auth_type,password=excluded.password,
 private_key=excluded.private_key,enabled=excluded.enabled,updated_at=excluded.updated_at`,
		cfg.ID, cfg.Name, cfg.Host, cfg.Port, cfg.Username, cfg.AuthType,
		cfg.Password, cfg.PrivateKey, boolToInt(cfg.Enabled), "", now, now)
	return err
}

func (r *SQLiteConnRepo) FindByID(ctx context.Context, id string) (*model.ConnectionConfig, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at FROM ssh_connection WHERE id=?`, id)
	return scanConn(row)
}

func (r *SQLiteConnRepo) FindByName(ctx context.Context, name string) (*model.ConnectionConfig, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at FROM ssh_connection WHERE name=?`, name)
	return scanConn(row)
}

func (r *SQLiteConnRepo) List(ctx context.Context) ([]*model.ConnectionConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at FROM ssh_connection ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.ConnectionConfig
	for rows.Next() {
		cfg, err := scanConnRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, cfg)
	}
	return result, nil
}

func (r *SQLiteConnRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ssh_connection WHERE id=?`, id)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanConn(row *sql.Row) (*model.ConnectionConfig, error) {
	var cfg model.ConnectionConfig
	var enabled int
	var lastConn, createdAt, updatedAt string
	err := row.Scan(&cfg.ID, &cfg.Name, &cfg.Host, &cfg.Port, &cfg.Username, &cfg.AuthType,
		&cfg.Password, &cfg.PrivateKey, &enabled, &lastConn, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg.Enabled = enabled == 1
	return &cfg, nil
}

func scanConnRows(rows *sql.Rows) (*model.ConnectionConfig, error) {
	var cfg model.ConnectionConfig
	var enabled int
	var lastConn, createdAt, updatedAt string
	err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.Host, &cfg.Port, &cfg.Username, &cfg.AuthType,
		&cfg.Password, &cfg.PrivateKey, &enabled, &lastConn, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	cfg.Enabled = enabled == 1
	return &cfg, nil
}
