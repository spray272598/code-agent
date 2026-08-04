package bootstrap

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/application"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/domain/codeindex"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/host"
	mcpsvc "github.com/spray272598/code-agent/internal/domain/mcp/service"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/slash"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/team"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/domain/worktree"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/einoorch"
	"github.com/spray272598/code-agent/internal/infrastructure/llm"
	inframcp "github.com/spray272598/code-agent/internal/infrastructure/mcp"
	"github.com/spray272598/code-agent/internal/infrastructure/mysql"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
	"github.com/spray272598/code-agent/internal/infrastructure/sqlite"
	"github.com/spray272598/code-agent/internal/infrastructure/storage"
	"github.com/spray272598/code-agent/internal/observability"
	"github.com/spray272598/code-agent/internal/trigger/ws"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

type App struct {
	Config  *config.Config
	Chat    *application.ChatApp
	Tools   *tool.MapRegistry
	Perm    *security.Guard
	Redis   *redisx.Client
	MCP     *inframcp.Manager
	Skills  *skill.Service
	Memory  *memory.Service
	Hooks   *hook.Bus
	Blobs   blob.Store
	Index   *codeindex.Index
	CKStore checkpoint.Store
	Runs    *checkpoint.RunRegistry
	Host    host.Executor
	Bridge  *host.Bridge
	HostHub *ws.HostHub
	Closer  func()
}

func Build(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}

	// Domain must not import infrastructure/observability — wire the port here.
	telemetry.Set(observability.DomainBridge{})

	var sessionRepo sessrepo.ISessionRepository
	var messageRepo sessrepo.IMessageRepository
	var memRepo memport.IMemoryRepository
	var auditRepo audit.Repository
	var summaryRepo sessrepo.ISummaryRepository
	var closer func()
	switch strings.ToLower(cfg.Database.Type) {
	case "mysql":
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
	case "sqlite", "sqlite3":
		path := cfg.Database.SQLitePath
		if path == "" {
			path = "./data/code-agent.db"
		}
		db, err := sqlite.Open(path, true) // always migrate lightweight schema
		if err != nil {
			log.Printf("[bootstrap] sqlite unavailable (%v), use memory\n", err)
			sessionRepo = repository.NewMemorySessionRepo()
			messageRepo = repository.NewMemoryMessageRepo()
			memRepo = repository.NewMemoryCoreRepo()
			auditRepo = repository.NewMemoryAuditRepo()
			summaryRepo = repository.NewMemorySummaryRepo()
			closer = func() {}
			cfg.Database.Type = "memory"
		} else {
			sessionRepo = repository.NewSQLiteSessionRepo(db)
			messageRepo = repository.NewSQLiteMessageRepo(db)
			memRepo = repository.NewSQLiteMemoryRepo(db)
			auditRepo = repository.NewSQLiteAuditRepo(db)
			summaryRepo = repository.NewSQLiteSummaryRepo(db)
			closer = func() { _ = db.Close() }
			log.Printf("[bootstrap] sqlite path=%s\n", path)
		}
	default:
		sessionRepo = repository.NewMemorySessionRepo()
		messageRepo = repository.NewMemoryMessageRepo()
		memRepo = repository.NewMemoryCoreRepo()
		auditRepo = repository.NewMemoryAuditRepo()
		summaryRepo = repository.NewMemorySummaryRepo()
		closer = func() {}
		cfg.Database.Type = "memory"
	}
	memSvc := memory.NewService(memRepo)
	memCtx := &coding.MemoryContext{Svc: memSvc}

	rdb := redisx.New(cfg.Redis)
	llmPort := llm.NewFromConfig(cfg)

	// host bridge always available for WS registration
	// API keys hashed in KeyStore — never pass plaintext into long-lived structs
	keyStore := auth.NewKeyStore(cfg.Security.APIKeys)
	hostBridge := host.NewBridge()
	hostHub := ws.NewHostHub(hostBridge, keyStore.Valid)
	log.Printf("[bootstrap] api keys configured=%d (hashed, not logged)\n", len(cfg.Security.APIKeys))

	var hostExec host.Executor = &host.ServerExecutor{Root: cfg.Agent.WorkspaceRoot}
	preferHost := cfg.Host.PreferHost || cfg.Host.Mode == "host"
	if preferHost {
		hostExec = &host.HostExecutor{Endpoint: cfg.Host.Endpoint, FallbackRoot: cfg.Agent.WorkspaceRoot}
		log.Printf("[bootstrap] host prefer_host=true (tools route to host-agent when online, fallback local)\n")
	}
	workspaceRoot := hostExec.WorkspaceRoot()

	// blob store: MinIO preferred, local fallback
	var blobStore blob.Store
	if cfg.Storage.Enabled {
		st, err := storage.NewStoreFromConfig(cfg.Storage)
		if err != nil {
			log.Printf("[bootstrap] blob store: %v\n", err)
		} else {
			blobStore = st
		}
	}

	ws := coding.NewWorkspace(workspaceRoot)
	reg := tool.NewRegistry()
	// local coding tools
	localRead := coding.NewReadFile(ws)
	localWrite := coding.NewWriteFile(ws)
	localEdit := coding.NewEditFile(ws)
	localBash := coding.NewBash(ws, 60)
	localGlob := coding.NewGlob(ws)
	localGrep := coding.NewGrep(ws)
	if preferHost {
		wrap := func(name, desc string, local tool.ITool) tool.ITool {
			return &host.ProxyTool{
				ToolName: name, Desc: desc, Local: local, Bridge: hostBridge,
				PreferHost: true, Timeout: 90 * time.Second,
			}
		}
		reg.Register(wrap("read_file", localRead.Description(), localRead))
		reg.Register(wrap("write_file", localWrite.Description(), localWrite))
		reg.Register(wrap("edit_file", localEdit.Description(), localEdit))
		reg.Register(wrap("bash", localBash.Description(), localBash))
		reg.Register(wrap("glob", localGlob.Description(), localGlob))
		reg.Register(wrap("grep", localGrep.Description(), localGrep))
	} else {
		reg.Register(localRead)
		reg.Register(localWrite)
		reg.Register(localEdit)
		reg.Register(localBash)
		reg.Register(localGlob)
		reg.Register(localGrep)
	}
	reg.Register(coding.NewMemorySave(memCtx))
	reg.Register(coding.NewMemorySearch(memCtx))

	// Code index / retriever tools
	codeIdx := codeindex.New(workspaceRoot)
	if st, err := codeIdx.Build(context.Background()); err != nil {
		log.Printf("[bootstrap] code index: %v\n", err)
	} else {
		log.Printf("[bootstrap] code index files=%d tokens=%d\n", st.Files, st.Tokens)
	}
	reg.Register(codeindex.NewSearchTool(codeIdx))
	reg.Register(codeindex.NewRebuildTool(codeIdx))

	// Durable checkpoint + in-process run cancel
	var ckStore checkpoint.Store
	if fs, errCK := checkpoint.NewFileStore("./data/checkpoints"); errCK != nil {
		log.Printf("[bootstrap] checkpoint file store: %v → memory\n", errCK)
		ckStore = checkpoint.NewMemoryStore()
	} else {
		ckStore = fs
	}
	runReg := checkpoint.NewRunRegistry()

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

	// Orchestrator: Eino is primary; native is offline/mock fallback only.
	// All tools (core + MCP) live in MapRegistry → Eino path wraps every tool with GuardedTool.
	var runner engine.Runner
	orch := strings.ToLower(strings.TrimSpace(cfg.Agent.Orchestrator))
	if orch == "" || orch == "eino" || orch == "default" {
		orch = "eino"
	}
	wantEino := orch == "eino"
	canEino := wantEino && cfg.LLM.APIKey != "" && !cfg.LLM.UseMock
	if wantEino && !canEino {
		log.Printf("[bootstrap] orchestrator=eino requested but LLM mock/no key → native (offline/mock)\n")
	}
	if canEino {
		er := einoorch.NewRunner(einoorch.Config{
			APIKey: cfg.LLM.APIKey, APIBase: cfg.LLM.APIBase, Model: cfg.LLM.Model,
			MaxSteps: cfg.Agent.MaxSteps, UseStream: cfg.Agent.EinoStream,
			TokenBudget:      cfg.Agent.TokenBudget,
			GraphResume:     cfg.EinoGraphResumeEnabled(),
			GraphCheckPointDir: cfg.Agent.EinoCheckPointDir,
		}, reg, perm, sessionRepo, messageRepo)
		er.SetHooks(hooks)
		er.SetAudit(auditRepo)
		er.SetSummaryRepo(summaryRepo)
		er.SetSkills(skillSvc)
		er.SetMemory(memSvc)
		er.SetCompressorLLM(contextx.NewSummarizer(llmPort))
		runner = er
		orch = "eino"
		log.Printf("[bootstrap] orchestrator=eino graph_resume=%v checkpoint_dir=%s | GuardedTool on ALL tools\n",
			cfg.EinoGraphResumeEnabled(), cfg.Agent.EinoCheckPointDir)
	} else {
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
		runner = loop
		orch = "native-offline"
		log.Printf("[bootstrap] orchestrator=native-offline (mock/no API key; Guard still on all tools)\n")
	}

	var chatOpts []application.Option
	chatOpts = append(chatOpts,
		application.WithSkills(skillSvc),
		application.WithMemory(memSvc),
		application.WithAudit(auditRepo),
		application.WithKeyStore(keyStore),
		application.WithCheckpoint(ckStore, runReg),
	)
	if blobStore != nil {
		chatOpts = append(chatOpts, application.WithBlobStore(blobStore))
	}
	if mcpMgr != nil {
		chatOpts = append(chatOpts, application.WithMCP(mcpMgr))
	}
	chat := application.New(application.CoreDeps{
		Loop: runner, Sessions: sessionRepo, Messages: messageRepo, Tools: reg, Perm: perm,
		Redis: rdb, TimeoutSec: cfg.Agent.TimeoutSec, Workspace: workspaceRoot,
		RateEnabled: cfg.RateLimit.Enabled, RatePerMin: cfg.RateLimit.PerMinute,
	}, chatOpts...)

	// rehydrate HITL pendings from durable checkpoints (cross-process interrupt)
	if n, err := chat.RestoreCheckpoints(context.Background()); err != nil {
		log.Printf("[bootstrap] restore checkpoints: %v\n", err)
	} else if n > 0 {
		log.Printf("[bootstrap] restored %d interrupt checkpoint(s)\n", n)
	}

	// slash: deep vs teams comparison + routing hints
	if s := chat.Slash(); s != nil {
		s.Register("deep", "DeepAgent Plan→Act→Reflect — chat: /deep <goal>", func(args string, _ slash.Context) slash.Result {
			goal := strings.TrimSpace(args)
			if goal == "" {
				return slash.Result{Handled: true, Response: "Usage: /deep <goal>\n\n" + deepagent.ComparisonDoc()}
			}
			// rewrite keeps /deep prefix so Eino Runner.looksDeep routes sequential phases
			return slash.Result{Handled: false, Rewrite: "/deep " + goal}
		})
		s.Register("compare-agents", "DeepAgent vs Teams comparison", func(string, slash.Context) slash.Result {
			return slash.Result{Handled: true, Response: deepagent.ComparisonDoc()}
		})
	}

	mcpN := 0
	for _, t := range reg.List() {
		if t != nil && strings.Contains(t.Name(), "__") {
			mcpN++
		}
	}
	log.Printf("[bootstrap] db=%s tools=%d (mcp=%d) redis=%v mock_llm=%v workspace=%s subagent=%v orchestrator=%s\n",
		cfg.Database.Type, len(reg.List()), mcpN, rdb.Enabled(), cfg.LLM.UseMock, cfg.Agent.WorkspaceRoot, subRunner != nil, orch)

	return &App{
		Config: cfg, Chat: chat, Tools: reg, Perm: perm, Redis: rdb,
		MCP: mcpMgr, Skills: skillSvc, Memory: memSvc, Hooks: hooks,
		Blobs: blobStore, Index: codeIdx, CKStore: ckStore, Runs: runReg,
		Host: hostExec, Bridge: hostBridge, HostHub: hostHub,
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
