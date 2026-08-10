package ssh

import (
	"context"
	"database/sql"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
)

type MySQLConnRepo struct{ db *sql.DB }

func NewMySQLConnRepo(db *sql.DB) *MySQLConnRepo { return &MySQLConnRepo{db: db} }

func (r *MySQLConnRepo) Save(ctx context.Context, cfg *model.ConnectionConfig) error {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err := r.db.ExecContext(ctx, `
INSERT INTO ssh_connection (id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE name=VALUES(name),host=VALUES(host),port=VALUES(port),
 username=VALUES(username),auth_type=VALUES(auth_type),password=VALUES(password),
 private_key=VALUES(private_key),enabled=VALUES(enabled),updated_at=VALUES(updated_at)`,
		cfg.ID, cfg.Name, cfg.Host, cfg.Port, cfg.Username, cfg.AuthType,
		cfg.Password, cfg.PrivateKey, cfg.Enabled, nil, now, now)
	return err
}

func (r *MySQLConnRepo) FindByID(ctx context.Context, id string) (*model.ConnectionConfig, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at FROM ssh_connection WHERE id=?`, id)
	return scanMySQLConn(row)
}

func (r *MySQLConnRepo) FindByName(ctx context.Context, name string) (*model.ConnectionConfig, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at FROM ssh_connection WHERE name=?`, name)
	return scanMySQLConn(row)
}

func (r *MySQLConnRepo) List(ctx context.Context) ([]*model.ConnectionConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,host,port,username,auth_type,password,private_key,enabled,last_connected_at,created_at,updated_at FROM ssh_connection ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.ConnectionConfig
	for rows.Next() {
		cfg, err := scanMySQLConnRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, cfg)
	}
	return result, nil
}

func (r *MySQLConnRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ssh_connection WHERE id=?`, id)
	return err
}

func scanMySQLConn(row *sql.Row) (*model.ConnectionConfig, error) {
	var cfg model.ConnectionConfig
	var lastConn sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&cfg.ID, &cfg.Name, &cfg.Host, &cfg.Port, &cfg.Username, &cfg.AuthType,
		&cfg.Password, &cfg.PrivateKey, &cfg.Enabled, &lastConn, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func scanMySQLConnRows(rows *sql.Rows) (*model.ConnectionConfig, error) {
	var cfg model.ConnectionConfig
	var lastConn sql.NullString
	var createdAt, updatedAt string
	err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.Host, &cfg.Port, &cfg.Username, &cfg.AuthType,
		&cfg.Password, &cfg.PrivateKey, &cfg.Enabled, &lastConn, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
