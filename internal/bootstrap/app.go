package bootstrap

import (
	"fmt"
	"log"

	"github.com/spray272598/code-agent/internal/application"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/llm"
	"github.com/spray272598/code-agent/internal/infrastructure/mysql"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
)

type App struct {
	Config *config.Config
	Chat   *application.ChatApp
	Tools  *tool.MapRegistry
	Perm   *security.Guard
	Redis  *redisx.Client
	Closer func()
}

func Build(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}

	// repos
	var sessionRepo sessrepo.ISessionRepository
	var messageRepo sessrepo.IMessageRepository
	var closer func()
	if cfg.Database.Type == "mysql" {
		db, err := mysql.Open(cfg.MySQLDSN(), cfg.Database.AutoMigrate, cfg.Database.SchemaPath)
		if err != nil {
			log.Printf("[bootstrap] mysql unavailable (%v), use memory\n", err)
			sessionRepo = repository.NewMemorySessionRepo()
			messageRepo = repository.NewMemoryMessageRepo()
			closer = func() {}
			cfg.Database.Type = "memory"
		} else {
			sessionRepo = repository.NewMySQLSessionRepo(db)
			messageRepo = repository.NewMySQLMessageRepo(db)
			closer = func() { _ = db.Close() }
		}
	} else {
		sessionRepo = repository.NewMemorySessionRepo()
		messageRepo = repository.NewMemoryMessageRepo()
		closer = func() {}
	}

	rdb := redisx.New(cfg.Redis)
	llmPort := llm.NewFromConfig(cfg)

	ws := coding.NewWorkspace(cfg.Agent.WorkspaceRoot)
	reg := tool.NewRegistry()
	reg.Register(coding.NewReadFile(ws))
	reg.Register(coding.NewWriteFile(ws))
	reg.Register(coding.NewEditFile(ws))
	reg.Register(coding.NewBash(ws, 60))
	reg.Register(coding.NewGlob(ws))
	reg.Register(coding.NewGrep(ws))

	perm := security.NewGuard(cfg.Agent.WorkspaceRoot, cfg.Security.PathSandbox, cfg.Security.DefaultConfirmWrite)
	loop := engine.NewLoop(llmPort, reg, sessionRepo, messageRepo, perm, cfg.Agent.MaxSteps, cfg.Agent.TokenBudget)
	chat := application.NewChatApp(
		loop, sessionRepo, messageRepo, reg, perm, rdb,
		cfg.Agent.TimeoutSec, cfg.Agent.WorkspaceRoot,
		cfg.RateLimit.Enabled, cfg.RateLimit.PerMinute, cfg.Security.APIKeys,
	)

	log.Printf("[bootstrap] db=%s tools=%d redis=%v mock_llm=%v workspace=%s\n",
		cfg.Database.Type, len(reg.List()), rdb.Enabled(), cfg.LLM.UseMock, cfg.Agent.WorkspaceRoot)

	return &App{
		Config: cfg, Chat: chat, Tools: reg, Perm: perm, Redis: rdb,
		Closer: func() {
			_ = rdb.Close()
			if closer != nil {
				closer()
			}
		},
	}, nil
}
