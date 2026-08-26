package orchestration

import (
	"fmt"
	"log"
)

// StorageType identifies the persistence backend for journal entries.
type StorageType string

const (
	StorageFile   StorageType = "file"   // Local JSONL file (default, single-instance)
	StorageMySQL  StorageType = "mysql"  // MySQL database (multi-instance, shared state)
	StorageRedis  StorageType = "redis"  // Redis sorted sets (high-throughput, low-latency)
	StorageMemory StorageType = "memory" // In-memory only (no persistence, for tests)
)

// JournalStorageConfig configures the journal persistence backend.
type JournalStorageConfig struct {
	// Type selects the storage backend (file/mysql/redis/memory).
	Type StorageType `json:"type" yaml:"type"`

	// FileDir is the base directory for file-based storage (default: os.TempDir()).
	FileDir string `json:"fileDir" yaml:"fileDir"`

	// MySQLDSN is the connection string for MySQL (user:pass@tcp(host:port)/db).
	MySQLDSN string `json:"mysqlDsn" yaml:"mysqlDsn"`

	// RedisAddr is the Redis server address (host:port).
	RedisAddr string `json:"redisAddr" yaml:"redisAddr"`
	// RedisPassword is the Redis password (empty for no auth).
	RedisPassword string `json:"redisPassword" yaml:"redisPassword"`
	// RedisDB is the Redis database number (default: 0).
	RedisDB int `json:"redisDb" yaml:"redisDb"`
}

// DefaultJournalStorageConfig returns sensible defaults (file-based).
func DefaultJournalStorageConfig() JournalStorageConfig {
	return JournalStorageConfig{
		Type:    StorageFile,
		FileDir: "",
	}
}

// NewJournalStorage creates a JournalStorage backend based on the config.
// Returns the storage backend and an error if the backend cannot be initialized.
func NewJournalStorage(cfg JournalStorageConfig, runID string) (JournalStorage, error) {
	switch cfg.Type {
	case StorageMySQL:
		if cfg.MySQLDSN == "" {
			return nil, fmt.Errorf("journal: MySQL storage requires MySQLDSN")
		}
		s, err := NewMySQLJournalStorage(cfg.MySQLDSN)
		if err != nil {
			return nil, fmt.Errorf("journal: init MySQL storage: %w", err)
		}
		log.Printf("[journal] using MySQL storage for run=%s", runID)
		return s, nil

	case StorageRedis:
		if cfg.RedisAddr == "" {
			return nil, fmt.Errorf("journal: Redis storage requires RedisAddr")
		}
		s, err := NewRedisJournalStorage(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
		if err != nil {
			return nil, fmt.Errorf("journal: init Redis storage: %w", err)
		}
		log.Printf("[journal] using Redis storage for run=%s", runID)
		return s, nil

	case StorageMemory:
		log.Printf("[journal] using in-memory storage for run=%s", runID)
		return NewMemoryJournalStorage(), nil

	case StorageFile, "":
		dir := cfg.FileDir
		if dir == "" {
			dir = "" // NewFileJournalStorage defaults to os.TempDir()
		}
		s, err := NewFileJournalStorage(dir, runID)
		if err != nil {
			return nil, fmt.Errorf("journal: init file storage: %w", err)
		}
		log.Printf("[journal] using file storage for run=%s dir=%s", runID, dir)
		return s, nil

	default:
		return nil, fmt.Errorf("journal: unknown storage type: %s", cfg.Type)
	}
}

// NewJournalWithConfig creates a Journal with the configured storage backend.
func NewJournalWithConfig(cfg JournalStorageConfig, runID string) (*Journal, error) {
	storage, err := NewJournalStorage(cfg, runID)
	if err != nil {
		return nil, err
	}
	return &Journal{storage: storage}, nil
}
