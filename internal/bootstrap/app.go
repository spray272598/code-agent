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
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/host"
	mcpsvc "github.com/spray272598/code-agent/internal/domain/mcp/service"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/team"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/domain/worktree"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/llm"
	inframcp "github.com/spray272598/code-agent/internal/infrastructure/mcp"
	"github.com/spray272598/code-agent/internal/infrastructure/mysql"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
	"github.com/spray272598/code-agent/internal/infrastructure/storage"
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
	Memory *memory.Service
	Hooks  *hook.Bus
	Blobs  blob.Store
	Host   host.Executor
	Closer func()
}

func Build(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}

	var sessionRepo sessrepo.ISessionRepository
	var messageRepo sessrepo.IMessageRepository
	var memRepo memport.IMemoryRepository
	var auditRepo audit.Repository
	var summaryRepo sessrepo.ISummaryRepository
	var closer func()
	if cfg.Database.Type == "mysql" {
		db, err := mysql.Open(cfg.MySQLDSN(), cfg.Database.AutoMigrate, cfg.Database.SchemaPath)
		if err != nil {
			log.Printf("[bootstrap] mysql unavailable (%v), use memory\n", err)
			sessionRepo = repository.NewMemorySessionRepo()
			messageRepo = repository.NewMemoryMessageRepo()
			memRepo = repository.NewMemoryCoreRepo()
			auditRepo = repository.NewMemoryAuditRepo()
			summaryRepo = repository.NewMemorySummaryRepo()
			closer = func() {}
			cfg.Database.Type = "memory"
		} else {
			sessionRepo = repository.NewMySQLSessionRepo(db)
			messageRepo = repository.NewMySQLMessageRepo(db)
			memRepo = repository.NewMySQLMemoryRepo(db)
			auditRepo = repository.NewMySQLAuditRepo(db)
			summaryRepo = repository.NewMySQLSummaryRepo(db)
			closer = func() { _ = db.Close() }
		}
	} else {
		sessionRepo = repository.NewMemorySessionRepo()
		messageRepo = repository.NewMemoryMessageRepo()
		memRepo = repository.NewMemoryCoreRepo()
		auditRepo = repository.NewMemoryAuditRepo()
		summaryRepo = repository.NewMemorySummaryRepo()
		closer = func() {}
	}
	memSvc := memory.NewService(memRepo)
	memCtx := &coding.MemoryContext{Svc: memSvc}

	rdb := redisx.New(cfg.Redis)
	llmPort := llm.NewFromConfig(cfg)

	// host executor mode (server default; host is stub roadmap)
	var hostExec host.Executor = &host.ServerExecutor{Root: cfg.Agent.WorkspaceRoot}
	if cfg.Host.Mode == "host" {
		hostExec = &host.HostExecutor{Endpoint: cfg.Host.Endpoint, FallbackRoot: cfg.Agent.WorkspaceRoot}
		log.Printf("[bootstrap] host executor mode=host endpoint=%s (side-car not wired; tools use fallback workspace)\n", cfg.Host.Endpoint)
	}
	workspaceRoot := hostExec.WorkspaceRoot()

	// blob store
	var blobStore blob.Store
	if cfg.Storage.Enabled {
		localDir := cfg.Storage.LocalFallbackDir
		if localDir == "" {
			localDir = "./data/objects"
		}
		ls, err := storage.NewLocalStore(localDir)
		if err != nil {
			log.Printf("[bootstrap] blob store: %v\n", err)
		} else {
			blobStore = ls
			log.Printf("[bootstrap] blob store local=%s\n", ls.Root())
		}
	}

	ws := coding.NewWorkspace(workspaceRoot)
	reg := tool.NewRegistry()
	reg.Register(coding.NewReadFile(ws))
	reg.Register(coding.NewWriteFile(ws))
	reg.Register(coding.NewEditFile(ws))
	reg.Register(coding.NewBash(ws, 60))
	reg.Register(coding.NewGlob(ws))
	reg.Register(coding.NewGrep(ws))
	reg.Register(coding.NewMemorySave(memCtx))
	reg.Register(coding.NewMemorySearch(memCtx))

	// SubAgent + worktree + teams
	var subRunner *subagent.Runner
	if cfg.SubAgent.Enabled {
		subRunner = subagent.NewRunner(llmPort, reg, workspaceRoot)
		if cfg.SubAgent.MaxConcurrent > 0 {
			subRunner.MaxConcurrent = cfg.SubAgent.MaxConcurrent
		}
		if cfg.SubAgent.DefaultSteps > 0 {
			subRunner.DefaultSteps = cfg.SubAgent.DefaultSteps
		}
		subRunner.Worktrees = worktree.NewManager(workspaceRoot)
		if cfg.Teams.Enabled && cfg.Teams.File != "" {
			if tc, err := team.LoadYAML(cfg.Teams.File); err == nil {
				team.ApplyToRunner(subRunner, tc)
				log.Printf("[bootstrap] team roles from %s\n", cfg.Teams.File)
			} else {
				log.Printf("[bootstrap] team yaml: %v\n", err)
			}
		}
		reg.Register(subagent.NewDelegateTool(subRunner))
	}

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

	perm := security.NewGuard(workspaceRoot, cfg.Security.PathSandbox, cfg.Security.DefaultConfirmWrite)
	loop := engine.NewLoop(llmPort, reg, sessionRepo, messageRepo, perm, cfg.Agent.MaxSteps, cfg.Agent.TokenBudget)
	loop.SetSkills(skillSvc)
	loop.SetHooks(hooks)
	loop.SetMemory(memSvc, memCtx)
	loop.SetAudit(auditRepo)
	loop.SetSummaryRepo(summaryRepo)
	if blobStore != nil {
		loop.SetBlobStore(blobStore, 4000)
	}
	if subRunner != nil {
		loop.SetSubRunner(subRunner)
	}

	chat := application.NewChatApp(
		loop, sessionRepo, messageRepo, reg, perm, rdb,
		cfg.Agent.TimeoutSec, workspaceRoot,
		cfg.RateLimit.Enabled, cfg.RateLimit.PerMinute, cfg.Security.APIKeys,
	)
	chat.SetSkills(skillSvc)
	chat.SetMemory(memSvc)
	chat.SetAudit(auditRepo)
	if blobStore != nil {
		chat.SetBlobStore(blobStore)
	}
	if mcpMgr != nil {
		chat.SetMCP(mcpMgr)
	}

	log.Printf("[bootstrap] db=%s tools=%d redis=%v mock_llm=%v workspace=%s mcp=%v subagent=%v\n",
		cfg.Database.Type, len(reg.List()), rdb.Enabled(), cfg.LLM.UseMock, cfg.Agent.WorkspaceRoot, mcpMgr != nil, subRunner != nil)

	return &App{
		Config: cfg, Chat: chat, Tools: reg, Perm: perm, Redis: rdb,
		MCP: mcpMgr, Skills: skillSvc, Memory: memSvc, Hooks: hooks,
		Blobs: blobStore, Host: hostExec,
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
