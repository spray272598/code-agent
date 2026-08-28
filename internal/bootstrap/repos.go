package bootstrap

import (
	"database/sql"
	"log"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/mysql"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
	"github.com/spray272598/code-agent/internal/infrastructure/sqlite"
)

// repos holds all repository instances created from the database.
type repos struct {
	SessionRepo  sessrepo.ISessionRepository
	MessageRepo  sessrepo.IMessageRepository
	MemRepo      memport.IMemoryRepository
	AuditRepo    audit.Repository
	SummaryRepo  sessrepo.ISummaryRepository
	UserRepo     auth.UserRepository
	DeviceRepo   auth.DeviceRepository
	RefreshRepo  auth.RefreshTokenRepository
	LLMKeyRepo   llmkey.Repository
	DB           *sql.DB
	Closer       func()
	dbType       string
}

// buildRepos creates all repositories based on the database config.
// Eliminates the 3x repeated switch pattern in Build().
func buildRepos(cfg *config.Config, sealer kms.CryptoSealer) repos {
	r := repos{
		SessionRepo:  repository.NewMemorySessionRepo(),
		MessageRepo:  repository.NewMemoryMessageRepo(),
		MemRepo:      repository.NewMemoryCoreRepo(),
		AuditRepo:    repository.NewMemoryAuditRepo(),
		SummaryRepo:  repository.NewMemorySummaryRepo(),
		UserRepo:     repository.NewMemoryUserRepo(),
		DeviceRepo:   repository.NewMemoryDeviceRepo(),
		RefreshRepo:  repository.NewMemoryRefreshTokenRepo(),
		Closer:       func() {},
		dbType:       "memory",
	}

	switch strings.ToLower(cfg.Database.Type) {
	case "mysql":
		r = buildMySQL(cfg, sealer)
	case "sqlite", "sqlite3":
		r = buildSQLite(cfg, sealer)
	default:
		r.LLMKeyRepo = repository.NewMemoryLLMKeyRepo(sealer)
	}

	return r
}

func buildMySQL(cfg *config.Config, sealer kms.CryptoSealer) repos {
	r := repos{dbType: "mysql"}
	opened, err := mysql.Open(cfg.MySQLDSN(), cfg.Database.AutoMigrate, cfg.Database.SchemaPath)
	if err != nil {
		log.Printf("[bootstrap] mysql unavailable (%v), use memory\n", err)
		r.SessionRepo = repository.NewMemorySessionRepo()
		r.MessageRepo = repository.NewMemoryMessageRepo()
		r.MemRepo = repository.NewMemoryCoreRepo()
		r.AuditRepo = repository.NewMemoryAuditRepo()
		r.SummaryRepo = repository.NewMemorySummaryRepo()
		r.UserRepo = repository.NewMemoryUserRepo()
		r.DeviceRepo = repository.NewMemoryDeviceRepo()
		r.RefreshRepo = repository.NewMemoryRefreshTokenRepo()
		r.LLMKeyRepo = repository.NewMemoryLLMKeyRepo(sealer)
		r.Closer = func() {}
		r.dbType = "memory"
		return r
	}

	r.DB = opened
	r.SessionRepo = repository.NewMySQLSessionRepo(opened)
	r.MessageRepo = repository.NewMySQLMessageRepo(opened)
	r.MemRepo = repository.NewMySQLMemoryRepo(opened)
	r.AuditRepo = repository.NewMySQLAuditRepo(opened)
	r.SummaryRepo = repository.NewMySQLSummaryRepo(opened)
	r.UserRepo = repository.NewMySQLUserRepo(opened)
	r.DeviceRepo = repository.NewMySQLDeviceRepo(opened)
	r.RefreshRepo = repository.NewMySQLRefreshTokenRepo(opened)
	r.LLMKeyRepo = repository.NewMySQLLLMKeyRepo(opened, sealer)
	r.Closer = func() { _ = opened.Close() }
	return r
}

func buildSQLite(cfg *config.Config, sealer kms.CryptoSealer) repos {
	r := repos{dbType: "sqlite"}
	path := cfg.Database.SQLitePath
	if path == "" {
		path = "./data/code-agent.db"
	}
	opened, err := sqlite.Open(path, true)
	if err != nil {
		log.Printf("[bootstrap] sqlite unavailable (%v), use memory\n", err)
		r.SessionRepo = repository.NewMemorySessionRepo()
		r.MessageRepo = repository.NewMemoryMessageRepo()
		r.MemRepo = repository.NewMemoryCoreRepo()
		r.AuditRepo = repository.NewMemoryAuditRepo()
		r.SummaryRepo = repository.NewMemorySummaryRepo()
		r.UserRepo = repository.NewMemoryUserRepo()
		r.DeviceRepo = repository.NewMemoryDeviceRepo()
		r.RefreshRepo = repository.NewMemoryRefreshTokenRepo()
		r.LLMKeyRepo = repository.NewMemoryLLMKeyRepo(sealer)
		r.Closer = func() {}
		r.dbType = "memory"
		return r
	}

	r.DB = opened
	r.SessionRepo = repository.NewSQLiteSessionRepo(opened)
	r.MessageRepo = repository.NewSQLiteMessageRepo(opened)
	r.MemRepo = repository.NewSQLiteMemoryRepo(opened)
	r.AuditRepo = repository.NewSQLiteAuditRepo(opened)
	r.SummaryRepo = repository.NewSQLiteSummaryRepo(opened)
	r.UserRepo = repository.NewSQLiteUserRepo(opened)
	r.DeviceRepo = repository.NewSQLiteDeviceRepo(opened)
	r.RefreshRepo = repository.NewSQLiteRefreshTokenRepo(opened)
	r.LLMKeyRepo = repository.NewSQLiteLLMKeyRepo(opened, sealer)
	r.Closer = func() { _ = opened.Close() }
	log.Printf("[bootstrap] sqlite path=%s\n", path)
	return r
}
