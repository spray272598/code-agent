package bootstrap

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spray272598/code-agent/internal/application"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/hook"
	mcpsvc "github.com/spray272598/code-agent/internal/domain/mcp/service"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/llm"
	inframcp "github.com/spray272598/code-agent/internal/infrastructure/mcp"
	"github.com/spray272598/code-agent/internal/infrastructure/mysql"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

type App struct {
	Config *config.Config
	Chat   *application.ChatApp
	Tools  *tool.MapRegistry
	Perm   *security.Guard
	Redis  *redisx.Client
	MCP    *inframcp.Manager
	Skills *skill.Service
	Hooks  *hook.Bus
	Closer func()
}

func Build(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}

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

	// hooks
	hooks := hook.NewBus()
	if cfg.Hooks.Enabled {
		hooks.RegisterDefaultLogger()
	}

	// skills
	var skillSvc *skill.Service
	if cfg.Skills.Enabled {
		skillSvc = skill.NewService(cfg.Skills.Dir)
		log.Printf("[bootstrap] skills=%d dir=%s\n", len(skillSvc.List()), skillSvc.RootDir())
	}

	// MCP (infra manager implements domain port)
	var mcpMgr *inframcp.Manager
	var mcpBridge *mcpsvc.ToolBridge
	if cfg.MCP.Enabled {
		mcpMgr = inframcp.NewManager()
		mcpBridge = mcpsvc.NewToolBridge(mcpMgr, reg)
		mcpMgr.OnToolsChanged(func(defs []model.ToolDef) {
			mcpBridge.ApplyDefs(defs)
		})
		// auto-load demo if present
		if demo := findMCPDemo(); demo != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := mcpMgr.AddOrUpdate(ctx, model.ServerConfig{
				Name: "demo", Transport: "stdio", Command: demo, Enabled: true, TimeoutSec: 30,
			})
			cancel()
			if err != nil {
				log.Printf("[bootstrap] mcp demo: %v\n", err)
			} else {
				log.Printf("[bootstrap] mcp demo loaded from %s\n", demo)
			}
		}
	}

	perm := security.NewGuard(cfg.Agent.WorkspaceRoot, cfg.Security.PathSandbox, cfg.Security.DefaultConfirmWrite)
	loop := engine.NewLoop(llmPort, reg, sessionRepo, messageRepo, perm, cfg.Agent.MaxSteps, cfg.Agent.TokenBudget)
	loop.SetSkills(skillSvc)
	loop.SetHooks(hooks)

	chat := application.NewChatApp(
		loop, sessionRepo, messageRepo, reg, perm, rdb,
		cfg.Agent.TimeoutSec, cfg.Agent.WorkspaceRoot,
		cfg.RateLimit.Enabled, cfg.RateLimit.PerMinute, cfg.Security.APIKeys,
	)
	chat.SetSkills(skillSvc)
	if mcpMgr != nil {
		chat.SetMCP(mcpMgr)
	}

	log.Printf("[bootstrap] db=%s tools=%d redis=%v mock_llm=%v workspace=%s mcp=%v\n",
		cfg.Database.Type, len(reg.List()), rdb.Enabled(), cfg.LLM.UseMock, cfg.Agent.WorkspaceRoot, mcpMgr != nil)

	return &App{
		Config: cfg, Chat: chat, Tools: reg, Perm: perm, Redis: rdb,
		MCP: mcpMgr, Skills: skillSvc, Hooks: hooks,
		Closer: func() {
			if mcpMgr != nil {
				_ = mcpMgr.Close()
			}
			_ = rdb.Close()
			if closer != nil {
				closer()
			}
		},
	}, nil
}

func findMCPDemo() string {
	cands := []string{"./mcp-demo", "./mcp-demo.exe", "./bin/mcp-demo", "./bin/mcp-demo.exe"}
	if ex, err := os.Executable(); err == nil {
		dir := filepath.Dir(ex)
		cands = append(cands, filepath.Join(dir, "mcp-demo"), filepath.Join(dir, "mcp-demo.exe"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}
