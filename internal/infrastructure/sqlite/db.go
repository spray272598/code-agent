package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) a SQLite database and optionally migrates schema.
func Open(path string, autoMigrate bool) (*sql.DB, error) {
	if path == "" {
		path = "./data/code-agent.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sqlite mkdir: %w", err)
	}
	// modernc driver; busy_timeout for concurrent readers
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite write serialization
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if autoMigrate {
		if err := migrate(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite migrate: %w", err)
		}
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chat_session (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '',
  agent_id TEXT NOT NULL DEFAULT 'code-agent',
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'ACTIVE',
  message_count INTEGER NOT NULL DEFAULT 0,
  token_used INTEGER NOT NULL DEFAULT 0,
  working_dir TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_sess_user ON chat_session(user_id, status)`,
		`CREATE TABLE IF NOT EXISTS chat_message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  tool_name TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  step INTEGER NOT NULL DEFAULT 0,
  token_count INTEGER NOT NULL DEFAULT 0,
  priority INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  FOREIGN KEY(session_id) REFERENCES chat_session(id) ON DELETE CASCADE
)`,
		`CREATE INDEX IF NOT EXISTS idx_msg_sess ON chat_message(session_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS session_summary (
  session_id TEXT PRIMARY KEY,
  summary TEXT NOT NULL,
  token_est INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS core_memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '',
  scope TEXT NOT NULL DEFAULT 'user',
  category TEXT NOT NULL DEFAULT 'general',
  content TEXT NOT NULL,
  importance INTEGER NOT NULL DEFAULT 50,
  source TEXT NOT NULL DEFAULT '',
  embedding TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_mem_user ON core_memory(user_id, scope)`,
		`ALTER TABLE core_memory ADD COLUMN embedding TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  tool TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  decision TEXT NOT NULL DEFAULT '',
  latency_ms INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS ssh_connection (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 22,
  username TEXT NOT NULL,
  auth_type TEXT NOT NULL DEFAULT 'password',
  password TEXT NOT NULL DEFAULT '',
  private_key TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  last_connected_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS organizations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  slug TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT 'free',
  owner_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_orgs_slug ON organizations(slug)`,
		`CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  email TEXT NOT NULL,
  password_hash TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'member',
  status TEXT NOT NULL DEFAULT 'pending',
  email_verified INTEGER NOT NULL DEFAULT 0,
  verify_token TEXT NOT NULL DEFAULT '',
  reset_token TEXT NOT NULL DEFAULT '',
  reset_expires_at TEXT,
  quota_tokens INTEGER,
  quota_reset_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_users_org_email ON users(org_id, email)`,
		`CREATE INDEX IF NOT EXISTS idx_users_verify ON users(verify_token)`,
		`CREATE INDEX IF NOT EXISTS idx_users_reset ON users(reset_token)`,
		`CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  user_code TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  org_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  scope TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL,
  last_poll_at TEXT,
  approved_at TEXT,
  created_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_user_code ON devices(user_code)`,
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT '',
  org_id TEXT NOT NULL DEFAULT '',
  device_id TEXT NOT NULL DEFAULT '',
  token_hash TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL,
  revoked INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_rt_user ON refresh_tokens(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_rt_token_hash ON refresh_tokens(token_hash)`,

		// Sprint 2.3: per-user LLM API keys (encrypted via KMS)
		`CREATE TABLE IF NOT EXISTS llm_keys (
  user_id TEXT NOT NULL,
  alias TEXT NOT NULL,
  provider TEXT NOT NULL,
  api_key_kms TEXT NOT NULL,
  api_base_kms TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, alias)
)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_keys_user ON llm_keys(user_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			// idempotent column add: ignore "duplicate column" when re-migrating
			if isDupColumnErr(err) {
				continue
			}
			return fmt.Errorf("%w\nstmt: %s", err, truncate(s, 80))
		}
	}
	return nil
}

func isDupColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate column")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
