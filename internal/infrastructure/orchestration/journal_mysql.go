package orchestration

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/spray272598/code-agent/internal/domain/orchestration"
)

func init() {
	orchestration.RegisterStorageFactory(orchestration.StorageMySQL, func(cfg orchestration.JournalStorageConfig, runID string) (orchestration.JournalStorage, error) {
		if cfg.MySQLDSN == "" {
			return nil, fmt.Errorf("journal: MySQL storage requires MySQLDSN")
		}
		s, err := NewMySQLJournalStorage(cfg.MySQLDSN)
		if err != nil {
			return nil, fmt.Errorf("journal: init MySQL storage: %w", err)
		}
		log.Printf("[journal] using MySQL storage for run=%s", runID)
		return s, nil
	})
}

// MySQLJournalStorage persists journal entries to a MySQL database.
// Suitable for multi-instance deployments where journal state must be shared.
//
// Schema (auto-created on first Append):
//
//	CREATE TABLE IF NOT EXISTS orchestration_journal (
//	  id BIGINT AUTO_INCREMENT PRIMARY KEY,
//	  run_id VARCHAR(255) NOT NULL,
//	  ts TIMESTAMP(3) NOT NULL,
//	  type VARCHAR(64) NOT NULL,
//	  phase_id VARCHAR(255) DEFAULT '',
//	  parent_id VARCHAR(255) DEFAULT '',
//	  content TEXT,
//	  token_delta INT DEFAULT 0,
//	  agent VARCHAR(255) DEFAULT '',
//	  error_msg TEXT,
//	  INDEX idx_run_id (run_id)
//	);
type MySQLJournalStorage struct {
	db *sql.DB
}

// NewMySQLJournalStorage connects to MySQL and ensures the journal table exists.
// dsn examples: "user:pass@tcp(localhost:3306)/code_agent?parseTime=true"
func NewMySQLJournalStorage(dsn string) (*MySQLJournalStorage, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	s := &MySQLJournalStorage{db: db}
	if err := s.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *MySQLJournalStorage) ensureSchema() error {
	ddl := `CREATE TABLE IF NOT EXISTS orchestration_journal (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  run_id VARCHAR(255) NOT NULL,
  ts TIMESTAMP(3) NOT NULL,
  type VARCHAR(64) NOT NULL,
  phase_id VARCHAR(255) DEFAULT '',
  parent_id VARCHAR(255) DEFAULT '',
  content TEXT,
  token_delta INT DEFAULT 0,
  agent VARCHAR(255) DEFAULT '',
  error_msg TEXT,
  INDEX idx_run_id (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("create journal table: %w", err)
	}
	return nil
}

func (s *MySQLJournalStorage) Append(entry orchestration.JournalEntry) error {
	entry.Timestamp = time.Now()
	_, err := s.db.Exec(
		`INSERT INTO orchestration_journal (run_id, ts, type, phase_id, parent_id, content, token_delta, agent, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.RunID,
		entry.Timestamp,
		string(entry.Type),
		entry.PhaseID,
		entry.ParentID,
		entry.Content,
		entry.TokenDelta,
		entry.Agent,
		entry.Error,
	)
	if err != nil {
		return fmt.Errorf("insert journal entry: %w", err)
	}
	return nil
}

func (s *MySQLJournalStorage) ReadAll(runID string) ([]orchestration.JournalEntry, error) {
	rows, err := s.db.Query(
		`SELECT run_id, ts, type, phase_id, parent_id, content, token_delta, agent, error_msg
		 FROM orchestration_journal WHERE run_id = ? ORDER BY ts ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("query journal: %w", err)
	}
	defer rows.Close()

	var entries []orchestration.JournalEntry
	for rows.Next() {
		var (
			e        orchestration.JournalEntry
			phaseID  sql.NullString
			parentID sql.NullString
			content  sql.NullString
			agent    sql.NullString
			errorMsg sql.NullString
		)
		if err := rows.Scan(
			&e.RunID,
			&e.Timestamp,
			(*string)(&e.Type),
			&phaseID,
			&parentID,
			&content,
			&e.TokenDelta,
			&agent,
			&errorMsg,
		); err != nil {
			return nil, fmt.Errorf("scan journal row: %w", err)
		}
		if phaseID.Valid {
			e.PhaseID = phaseID.String
		}
		if parentID.Valid {
			e.ParentID = parentID.String
		}
		if content.Valid {
			e.Content = content.String
		}
		if agent.Valid {
			e.Agent = agent.String
		}
		if errorMsg.Valid {
			e.Error = errorMsg.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate journal rows: %w", err)
	}
	return entries, nil
}

func (s *MySQLJournalStorage) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

var _ orchestration.JournalStorage = (*MySQLJournalStorage)(nil)
