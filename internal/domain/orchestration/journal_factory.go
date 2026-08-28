package orchestration

import (
	"fmt"
	"sync"
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

// StorageFactory creates a JournalStorage from config.
type StorageFactory func(cfg JournalStorageConfig, runID string) (JournalStorage, error)

// storageRegistry holds registered storage factories (infrastructure implementations).
var (
	storageMu       sync.RWMutex
	storageRegistry = map[StorageType]StorageFactory{}
)

// RegisterStorageFactory registers a storage factory for a given type.
// Called by infrastructure packages during init.
func RegisterStorageFactory(t StorageType, f StorageFactory) {
	storageMu.Lock()
	defer storageMu.Unlock()
	storageRegistry[t] = f
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
	case StorageMySQL, StorageRedis:
		storageMu.RLock()
		factory, ok := storageRegistry[cfg.Type]
		storageMu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("journal: storage type %s not registered (import infrastructure/orchestration)", cfg.Type)
		}
		return factory(cfg, runID)

	case StorageMemory:
		return NewMemoryJournalStorage(), nil

	case StorageFile, "":
		dir := cfg.FileDir
		s, err := NewFileJournalStorage(dir, runID)
		if err != nil {
			return nil, fmt.Errorf("journal: init file storage: %w", err)
		}
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
