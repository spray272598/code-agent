package bootstrap

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/application"
	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/domain/codeindex"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/host"
	"github.com/spray272598/code-agent/internal/domain/intent"
	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
	mcpcache "github.com/spray272598/code-agent/internal/domain/mcp/cache"
	mcphealth "github.com/spray272598/code-agent/internal/domain/mcp/health"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
	mcpsvc "github.com/spray272598/code-agent/internal/domain/mcp/service"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/slash"
	"github.com/spray272598/code-agent/internal/domain/spec"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	"github.com/spray272598/code-agent/internal/domain/sshtool"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/team"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	vectordomain "github.com/spray272598/code-agent/internal/domain/vector"
	"github.com/spray272598/code-agent/internal/domain/worktree"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
	"github.com/spray272598/code-agent/internal/infrastructure/einoorch"
	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
	"github.com/spray272598/code-agent/internal/infrastructure/llm"
	"github.com/spray272598/code-agent/internal/infrastructure/logger"
	inframcp "github.com/spray272598/code-agent/internal/infrastructure/mcp"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	lsandbox "github.com/spray272598/code-agent/internal/infrastructure/sandbox/linux"
	skillmarket "github.com/spray272598/code-agent/internal/infrastructure/skill"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
	"github.com/spray272598/code-agent/internal/infrastructure/storage"
	vectorinfra "github.com/spray272598/code-agent/internal/infrastructure/vector"
	"github.com/spray272598/code-agent/internal/infrastructure/vector/qdrant"
	"github.com/spray272598/code-agent/internal/observability"
	wshub "github.com/spray272598/code-agent/internal/trigger/ws"

	// Register infrastructure storage implementations (init() side effects).
	_ "github.com/spray272598/code-agent/internal/infrastructure/orchestration"
)

// builder carries every intermediate value constructed during bootstrap so the
// wiring phases can be split into focused methods instead of one giant Build.
// Each field mirrors a dependency that was previously a local variable inside Build.
type builder struct {
	cfg   *config.Config
	repos repos

	sealer        kms.CryptoSealer
	memSvc        *memory.Service
	memCtx        *coding.MemoryContext
	rdb           *redisx.Client
	llmPort       port.ILLMPort
	keyStore      *auth.KeyStore
	hostBridge    *host.Bridge
	hostHub       *wshub.HostHub
	workspaceRoot string
	perm          *security.Guard
	blobStore     blob.Store
	reg           *tool.MapRegistry
	codeIdx       *codeindex.Index
	ckStore       checkpoint.Store
	runReg        *checkpoint.RunRegistry
	subRunner     *subagent.Runner
	hooks         *hook.Bus
	skillSvc      *skill.Service
	specSvc       *spec.Service
	mcpFactory    *inframcp.UserFactory
	mcpBridge     *mcpsvc.ToolBridge
	mcpHealth     *mcphealth.MCPHealthMonitor
	sshPool       *sshinfra.Pool
	sshRepo       sshport.IConnectionRepository
	runner        engine.Runner
	hostExec      host.Executor
	sshTermHub    *wshub.SSHTerminalHub
	orch          string
}

// App is the fully wired composition root returned by Build.
type App struct {
	Config         *config.Config
	Chat           *application.ChatApp
	Tools          *tool.MapRegistry
	Perm           *security.Guard
	Redis          *redisx.Client
	MCP            *inframcp.UserFactory
	MCPHealth      *mcphealth.MCPHealthMonitor
	Skills         *skill.Service
	Memory         *memory.Service
	Hooks          *hook.Bus
	KMS            kms.CryptoSealer
	LLMKey         llmkey.Repository
	Blobs          blob.Store
	Index          *codeindex.Index
	CKStore        checkpoint.Store
	Runs           *checkpoint.RunRegistry
	Host           host.Executor
	Bridge         *host.Bridge
	HostHub        *wshub.HostHub
	SSHTerminalHub *wshub.SSHTerminalHub
	SSHPool        *sshinfra.Pool

	// Account repos (Sprint 1.1)
	UserRepo    auth.UserRepository
	DeviceRepo  auth.DeviceRepository
	RefreshRepo auth.RefreshTokenRepository

	Closer func()
}

// wireFoundation initializes logging/telemetry, the KMS sealer, all repositories,
// the LLM port, the host bridge/hub, the workspace, the permission Guard, and the
// optional Linux kernel sandbox enforcer.
func (b *builder) wireFoundation() {
	cfg := b.cfg

	// Initialize structured logging (slog).
	logLevel := cfg.Logging.Level
	if logLevel == "" {
		logLevel = "info"
	}
	logger.Init(logLevel, "text")

	// Domain must not import infrastructure/observability — wire the port here.
	telemetry.Set(observability.DomainBridge{})

	// Initialize OTel Metrics if enabled (replaces custom Prometheus counters).
	if cfg.OTLP.Enabled && cfg.OTLP.MetricsEnabled {
		shutdown, err := observability.SetupOTelMetrics(context.Background(), "code-agent")
		if err != nil {
			log.Printf("[bootstrap] otel metrics init failed: %v (fallback atomic counters)\n", err)
		} else {
			_ = shutdown
			log.Printf("[bootstrap] otel metrics enabled\n")
		}
	}

	// Sprint 2.8: KMS sealer (AES-256-GCM). Constructed once at boot; all
	// encrypting repos (SSH, LLM Key) share the same sealer.
	sealer, err := kmsinfra.NewSealer()
	if err != nil {
		log.Fatalf("[bootstrap] kms sealer: %v", err)
	}
	b.sealer = sealer
	log.Printf("[bootstrap] kms sealer active key id=%s\n", sealer.KeyID())

	// Build all repositories (eliminates 3x repeated switch pattern).
	b.repos = buildRepos(cfg, sealer)
	cfg.Database.Type = b.repos.dbType

	// Memory service
	b.memSvc = memory.NewService(b.repos.MemRepo)
	b.memCtx = &coding.MemoryContext{Svc: b.memSvc}

	b.rdb = redisx.New(cfg.Redis)
	b.llmPort = llm.NewFromConfig(cfg)

	// host bridge always available for WS registration
	// API keys hashed in KeyStore — never pass plaintext into long-lived structs
	b.keyStore = auth.NewKeyStore(cfg.Security.APIKeys)
	b.hostBridge = host.NewBridge()
	b.hostHub = wshub.NewHostHub(b.hostBridge, b.keyStore.Valid)
	log.Printf("[bootstrap] api keys configured=%d (hashed, not logged)\n", len(cfg.Security.APIKeys))

	b.hostExec = &host.ServerExecutor{Root: cfg.Agent.WorkspaceRoot}
	preferHost := cfg.Host.PreferHost || cfg.Host.Mode == "host"
	if preferHost {
		b.hostExec = &host.HostExecutor{Endpoint: cfg.Host.Endpoint, FallbackRoot: cfg.Agent.WorkspaceRoot}
		log.Printf("[bootstrap] host prefer_host=true (tools route to host-agent when online, fallback local)\n")
	}
	b.workspaceRoot = b.hostExec.WorkspaceRoot()

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
	b.blobStore = blobStore

	b.perm = security.NewGuardMode(b.workspaceRoot, cfg.Security.PathSandbox, cfg.Security.DefaultConfirmWrite, security.ParseSandboxMode(cfg.Security.SandboxMode))

	// Inject enhanced sandbox enforcer (Linux kernel-level sandbox with Landlock, seccomp, namespaces, cgroups)
	var enhancedEnforcer security.SandboxEnforcer
	if runtime.GOOS == "linux" {
		enhancedEnforcer = lsandbox.NewEnhancedSandboxEnforcer(b.perm.Audit())
		if err := enhancedEnforcer.ApplyProfile(security.ProfileConfig{
			NetworkBlock: b.perm.Mode() == security.ModeStrict,
			Deny:         []string{"**/.env", "**/*.pem", "**/secrets/**"},
		}, b.workspaceRoot); err != nil {
			log.Printf("[bootstrap] enhanced sandbox init failed, falling back: %v\n", err)
			enhancedEnforcer = nil
		} else {
			log.Printf("[bootstrap] enhanced kernel sandbox active: landlock+seccomp+namespaces+cgroups\n")
		}
	}

	// Inject enhanced sandbox into Guard (dependency inversion)
	if enhancedEnforcer != nil {
		b.perm.SetExternalSandboxEnforcer(enhancedEnforcer)
	}
}

// wireTools registers the local/expanded/SSH tool set, the code index, the
// checkpoint store, the sub-agent runner, hooks, skills and the spec service.
func (b *builder) wireTools() {
	cfg := b.cfg
	ws := coding.NewWorkspace(b.workspaceRoot)

	reg := tool.NewRegistry()
	// local coding tools
	localRead := coding.NewReadFile(ws)
	localWrite := coding.NewWriteFile(ws)
	localEdit := coding.NewEditFile(ws)
	localBash := coding.NewBash(ws, 60)
	localGlob := coding.NewGlob(ws)
	localGrep := coding.NewGrep(ws)
	preferHost := cfg.Host.PreferHost || cfg.Host.Mode == "host"
	if preferHost {
		wrap := func(name, desc string, local tool.ITool) tool.ITool {
			return &host.ProxyTool{
				ToolName: name, Desc: desc, Local: local, Bridge: b.hostBridge,
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
	// expanded tool set (apply_patch / lint / codecov / web_search)
	reg.Register(coding.NewApplyPatch(ws))
	reg.Register(coding.NewLint(ws))
	reg.Register(coding.NewCodecov(ws))
	reg.Register(coding.NewWebSearch(ws, nil))
	reg.Register(coding.NewSwitchWorkspace(ws, b.perm))
	reg.Register(coding.NewMemorySave(b.memCtx))
	reg.Register(coding.NewMemorySearch(b.memCtx))

	// SSH remote operations
	if cfg.SSH.Enabled {
		sshPool := sshinfra.NewPool()
		var sshRepo sshport.IConnectionRepository
		if b.repos.DB != nil {
			var raw sshport.IConnectionRepository
			switch {
			case "mysql" == lowerDB(cfg.Database.Type):
				raw = sshinfra.NewMySQLConnRepo(b.repos.DB)
			default:
				raw = sshinfra.NewSQLiteConnRepo(b.repos.DB)
			}
			// Sprint 2.9: wrap the raw SSH repo so Password/PrivateKey are
			// stored as KMS ciphertext. Fail-closed: the decorator propagates
			// any KMS error rather than silently downgrading to plaintext.
			sshRepo = sshinfra.NewEncryptingConnRepo(raw, b.sealer)
		}
		if sshRepo != nil {
			// auto-load saved connections
			conns, _ := sshRepo.List(context.Background())
			for _, c := range conns {
				if c.Enabled {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					if err := sshPool.Connect(ctx, *c); err != nil {
						log.Printf("[bootstrap] ssh connect %s: %v\n", c.Name, err)
					}
					cancel()
				}
			}
		}
		sshExec := sshinfra.NewExecutor(sshPool)
		sshFT := sshinfra.NewFileTransfer(sshPool)
		sshTerm := sshinfra.NewTerminal(sshPool)
		sshtool.RegisterAll(reg, sshExec, sshFT, sshTerm)
		log.Printf("[bootstrap] ssh enabled, tools registered\n")
		b.sshPool = sshPool
		b.sshRepo = sshRepo
	}

	// Code index / retriever tools
	codeIdx := codeindex.New(b.workspaceRoot)
	if st, errCK := codeIdx.Build(context.Background()); errCK != nil {
		log.Printf("[bootstrap] code index: %v\n", errCK)
	} else {
		log.Printf("[bootstrap] code index files=%d tokens=%d\n", st.Files, st.Tokens)
	}
	reg.Register(codeindex.NewSearchTool(codeIdx))
	reg.Register(codeindex.NewRebuildTool(codeIdx))
	b.codeIdx = codeIdx

	// Durable checkpoint + in-process run cancel
	var ckStore checkpoint.Store
	if fs, errCK := checkpoint.NewFileStore("./data/checkpoints"); errCK != nil {
		log.Printf("[bootstrap] checkpoint file store: %v → memory\n", errCK)
		ckStore = checkpoint.NewMemoryStore()
	} else {
		ckStore = fs
	}
	runReg := checkpoint.NewRunRegistry()
	b.ckStore = ckStore
	b.runReg = runReg

	// SubAgent + worktree + teams
	if cfg.SubAgent.Enabled {
		subRunner := subagent.NewRunner(b.llmPort, reg, b.workspaceRoot)
		if cfg.SubAgent.MaxConcurrent > 0 {
			subRunner.MaxConcurrent = cfg.SubAgent.MaxConcurrent
		}
		if cfg.SubAgent.DefaultSteps > 0 {
			subRunner.DefaultSteps = cfg.SubAgent.DefaultSteps
		}
		subRunner.Worktrees = worktree.NewManager(b.workspaceRoot)
		if cfg.Teams.Enabled && cfg.Teams.File != "" {
			if tc, err := team.LoadYAML(cfg.Teams.File); err == nil {
				team.ApplyToRunner(subRunner, tc)
				log.Printf("[bootstrap] team roles from %s\n", cfg.Teams.File)
			} else {
				log.Printf("[bootstrap] team yaml: %v\n", err)
			}
		}
		reg.Register(subagent.NewDelegateTool(subRunner))
		b.subRunner = subRunner
	}

	// hooks
	hooks := hook.NewBus()
	if cfg.Hooks.Enabled {
		hooks.RegisterDefaultLogger()
	}
	b.hooks = hooks

	// skills
	if cfg.Skills.Enabled {
		skillSvc := skill.NewService(cfg.Skills.Dir)
		skillSvc.SetMarketplace(skill.NewLocalMarketplace(cfg.Skills.MarketDir))
		// Remote skill marketplace (HTTP registry with optional signature verification).
		if cfg.Skills.RemoteURL != "" {
			var marketOpts []skillmarket.Option
			if cfg.Skills.PublicKeyPath != "" {
				pubKey, err := skillmarket.LoadEd25519PublicKey(cfg.Skills.PublicKeyPath)
				if err != nil {
					log.Printf("[bootstrap] skill remote pubkey: %v\n", err)
				} else {
					marketOpts = append(marketOpts, skillmarket.WithEd25519PublicKey(pubKey))
				}
			}
			remoteMkt := skillmarket.NewRemoteMarketplace(cfg.Skills.RemoteURL, marketOpts...)
			skillSvc.SetMarketplace(remoteMkt)
			log.Printf("[bootstrap] skill remote marketplace: %s\n", cfg.Skills.RemoteURL)
		}
		// Register skill tools into the agent registry so skills become
		// directly invocable as tools (e.g. skill_deploy, skill_review).
		if skillTools := skillSvc.BuildSkillTools(); len(skillTools) > 0 {
			for _, st := range skillTools {
				reg.Register(st)
			}
			log.Printf("[bootstrap] skills=%d dir=%s market=%s tools_registered=%d\n",
				len(skillSvc.List()), skillSvc.RootDir(), cfg.Skills.MarketDir, len(skillTools))
		} else {
			log.Printf("[bootstrap] skills=%d dir=%s market=%s\n",
				len(skillSvc.List()), skillSvc.RootDir(), cfg.Skills.MarketDir)
		}
		b.skillSvc = skillSvc
	}

	// spec-driven development: load spec.md/tasks.md/checklist.md/CLAUDE.md from workspace root
	specSvc := spec.NewService(b.workspaceRoot)
	if specSvc.HasSpec() || specSvc.HasCLAUDE() {
		log.Printf("[bootstrap] spec loaded: title=%q has_spec=%v has_claude=%v progress=%.0f%%\n",
			specSvc.GetTitle(), specSvc.HasSpec(), specSvc.HasCLAUDE(), specSvc.Progress())
	}
	b.specSvc = specSvc

	b.reg = reg
}

// lowerDB normalizes the configured database type for switch comparisons.
func lowerDB(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// wireMemoryEmbedding wires the LLM-backed memory extractor, the embedding
// backend (MemIndex by default, Qdrant when configured) and semantic skill
// matching. Kept separate from wireTools because it depends on the embedding
// provider config and backfills asynchronously.
func (b *builder) wireMemoryEmbedding() {
	cfg := b.cfg

	// semantic memory extraction (LLM-backed; falls back to rules when unavailable)
	b.memSvc.SetExtractor(memory.NewLLMExtractor(b.llmPort))
	// embedding (shared by memory search, skill matching, code index)
	var embedder port.IEmbeddingPort
	// vecIdx backs the dense-vector layer (memory search + code RAG). MemIndex is
	// the safe in-process default; provider "qdrant" plugs the same
	// IVectorIndex interface into a remote Qdrant instance for persistence/scale.
	var vecIdx vectordomain.IVectorIndex = vectorinfra.NewMemIndex()
	if cfg.LLM.EmbeddingEnabled {
		embedder = llm.NewOpenAIEmbedding(cfg.LLM.APIKey, cfg.LLM.EmbeddingAPIBase, cfg.LLM.EmbeddingModel)
		b.memSvc.SetEmbedder(embedder)
		log.Printf("[bootstrap] embedding enabled model=%s\n", cfg.LLM.EmbeddingModel)
		// backfill stored memories that predate embedding
		if n := b.memSvc.Backfill(context.Background(), 500); n > 0 {
			log.Printf("[bootstrap] memory embedding backfilled %d item(s)\n", n)
		}
		// Sprint 2.1: select the dense-vector backend. Default MemIndex keeps the
		// process self-contained; provider "qdrant" switches the shared interface
		// to a remote Qdrant instance (collections "memories" + "code").
		if cfg.Vector.Provider == "qdrant" {
			dim := cfg.Vector.Dimension
			if dim <= 0 {
				dim = embedder.Dims()
			}
			q, qerr := qdrant.New(cfg.Vector.QdrantURL, cfg.Vector.QdrantAPIKey, dim, 10*time.Second)
			if qerr != nil {
				log.Printf("[bootstrap] qdrant init failed: %v (fallback MemIndex)\n", qerr)
			} else if eerr := q.Ensure(context.Background(), cfg.Vector.Collection, dim); eerr != nil {
				log.Printf("[bootstrap] qdrant ensure failed: %v (fallback MemIndex)\n", eerr)
			} else {
				vecIdx = q
				// memory uses the "memories" collection; ensure it up front.
				_ = vecIdx.Ensure(context.Background(), "memories", dim)
				log.Printf("[bootstrap] vector backend=qdrant collection=%s dim=%d\n", cfg.Vector.Collection, dim)
			}
		}
		// wire both memory and code-index to the chosen backend.
		b.codeIdx.SetEmbedder(embedder)
		b.codeIdx.SetVectorIndex(vecIdx, "code")
		// async code-index semantic vectors + chunk-level RAG index (non-blocking startup)
		go func() {
			if n := b.codeIdx.BuildEmbeddings(context.Background(), 300); n > 0 {
				log.Printf("[bootstrap] code index embedded %d file(s)\n", n)
			}
		}()
	} else {
		log.Printf("[bootstrap] embedding disabled (set llm.embedding_model to enable)\n")
	}

	// Sprint 1.10/1.11: wire the in-process (or remote) dense-vector backend to
	// memory search. vecIdx is MemIndex by default and Qdrant when configured.
	b.memSvc.SetVectorIndex(vecIdx, "memories")
	if cfg.LLM.EmbeddingEnabled {
		if n := b.memSvc.BackfillVector(context.Background(), 500); n > 0 {
			log.Printf("[bootstrap] vector backfill indexed %d memory/ies\n", n)
		}
	}
	// semantic skill matching: vector fast-path + LLM fallback
	if b.skillSvc != nil {
		b.skillSvc.SetLLM(b.llmPort)
		b.skillSvc.SetEmbedder(embedder)
	}
}

// wireMCP builds the per-user MCP factory, the tool bridge and the health
// monitor, and seeds the system tenant from mcp.json / the demo binary.
func (b *builder) wireMCP() {
	cfg := b.cfg
	if !cfg.MCP.Enabled {
		return
	}

	mcpFactory := inframcp.NewUserFactory(func(userID string) *inframcp.Manager {
		return inframcp.NewUserManager(userID)
	})
	// system manager: bootstrap-loaded servers (cfg.MCP.ConfigFile, demo)
	sysMgr := inframcp.NewUserManager("")
	// ToolCache with 30s TTL / 256 entries for deduplicating read-only MCP calls.
	mcpCache := mcpcache.NewToolCache(30*time.Second, 256)
	mcpBridge := mcpsvc.NewToolBridgeWithFactory(mcpFactory, b.reg).WithCache(mcpCache)
	// Health monitor with background PING checks every 15s.
	mcpHealth := mcphealth.NewMCPHealthMonitor(15*time.Second, func(ctx context.Context, name string) error {
		mgr, err := mcpFactory.ForUserID("")
		if err != nil {
			return err
		}
		if mgr.IsOnline(name) {
			return nil
		}
		return fmt.Errorf("server %s offline", name)
	})
	sysMgr.OnToolsChanged(func(defs []model.ToolDef) {
		mcpBridge.ApplyDefs(defs)
		// Update per-server tool counts for observability.
		byServer := map[string]int{}
		for _, d := range defs {
			byServer[d.ServerName]++
		}
		for name, count := range byServer {
			mcpHealth.UpdateToolCount(name, count)
		}
	})
	// prime the cache with the system manager under the "" key so
	// ForUserID("") returns the seeded one
	mcpFactory.PrimeSystem(sysMgr)
	// auto-load servers from mcp.json (VS Code style) if configured
	if cfg.MCP.ConfigFile != "" {
		servers, err := inframcp.LoadServersFromFile(cfg.MCP.ConfigFile)
		if err != nil {
			log.Printf("[bootstrap] mcp config %s: %v\n", cfg.MCP.ConfigFile, err)
		} else {
			for _, sc := range servers {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				err := sysMgr.AddOrUpdate(ctx, sc)
				cancel()
				if err != nil {
					log.Printf("[bootstrap] mcp server %s: %v\n", sc.Name, err)
				} else {
					log.Printf("[bootstrap] mcp server loaded: %s (transport=%s)\n", sc.Name, sc.Transport)
					mcpHealth.RegisterServer(sc)
				}
			}
		}
	}
	// auto-load demo if present
	if demo := findMCPDemo(); demo != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		demoCfg := model.ServerConfig{
			Name: "demo", Transport: "stdio", Command: demo, Enabled: true, TimeoutSec: 30,
		}
		err := sysMgr.AddOrUpdate(ctx, demoCfg)
		cancel()
		if err != nil {
			log.Printf("[bootstrap] mcp demo: %v\n", err)
		} else {
			log.Printf("[bootstrap] mcp demo loaded from %s\n", demo)
			mcpHealth.RegisterServer(demoCfg)
		}
	}
	// Start MCP health monitoring in background.
	go mcpHealth.Start(context.Background())
	log.Printf("[bootstrap] mcp health monitor started (interval=15s)\n")
	// Wire MCP health into host heartbeat for combined monitoring.
	if b.hostBridge.HeartbeatManager() != nil {
		b.hostBridge.SetMCPHealthReporter(mcpHealth)
		log.Printf("[bootstrap] mcp health → heartbeat integration active\n")
	} else {
		log.Printf("[bootstrap] mcp health → heartbeat skipped (no heartbeat manager)\n")
	}
	// MCP config hot-reload: watch mcp.json for changes and auto-reconnect.
	if cfg.MCP.HotReload && cfg.MCP.ConfigFile != "" {
		mcpWatcher := inframcp.NewConfigWatcher(cfg.MCP.ConfigFile,
			func(ctx context.Context, path string) error {
				log.Printf("[bootstrap] mcp config changed, reloading %s\n", path)
				servers, err := inframcp.LoadServersFromFile(path)
				if err != nil {
					return fmt.Errorf("reload mcp config: %w", err)
				}
				for _, sc := range servers {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					err := sysMgr.AddOrUpdate(ctx, sc)
					cancel()
					if err != nil {
						log.Printf("[bootstrap] mcp hot-reload server %s: %v\n", sc.Name, err)
					} else {
						log.Printf("[bootstrap] mcp hot-reload server: %s\n", sc.Name)
						mcpHealth.RegisterServer(sc)
					}
				}
				return nil
			},
		)
		mcpWatcher.Start(context.Background())
		defer mcpWatcher.Stop()
	}

	b.mcpFactory = mcpFactory
	b.mcpBridge = mcpBridge
	b.mcpHealth = mcpHealth
}

// wireRunner selects the orchestrator: Eino is primary; the native loop is the
// offline/mock fallback. All tools (core + MCP) are wrapped with GuardedTool by
// the runner.
func (b *builder) wireRunner() {
	cfg := b.cfg

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
			TokenBudget:           cfg.Agent.TokenBudget,
			CompactThresholdRatio: cfg.Agent.CompactThresholdRatio,
			GraphResume:           cfg.EinoGraphResumeEnabled(),
			GraphCheckPointDir:    cfg.Agent.EinoCheckPointDir,
			Router:                cfg.LLM.ToRoutes(),
		}, b.reg, b.perm, b.repos.SessionRepo, b.repos.MessageRepo)
		er.SetHooks(b.hooks)
		er.SetAudit(b.repos.AuditRepo)
		er.SetSummaryRepo(b.repos.SummaryRepo)
		er.SetSkills(b.skillSvc)
		er.SetMemory(b.memSvc)
		er.SetSpecService(b.specSvc)
		intentClassifier := intent.NewClassifier(nil)
		intentClassifier.SetLLM(b.llmPort)
		er.SetIntentRouter(intentClassifier)
		er.SetCompressorLLM(contextx.NewSummarizer(b.llmPort))
		runner = er
		orch = "eino"
		log.Printf("[bootstrap] orchestrator=eino graph_resume=%v checkpoint_dir=%s | GuardedTool on ALL tools\n",
			cfg.EinoGraphResumeEnabled(), cfg.Agent.EinoCheckPointDir)
	} else {
		loop := engine.NewLoop(b.llmPort, b.reg, b.repos.SessionRepo, b.repos.MessageRepo, b.perm, cfg.Agent.MaxSteps, cfg.Agent.TokenBudget)
		loop.SetSkills(b.skillSvc)
		loop.SetHooks(b.hooks)
		loop.SetMemory(b.memSvc, b.memCtx)
		loop.SetAudit(b.repos.AuditRepo)
		loop.SetSummaryRepo(b.repos.SummaryRepo)
		loop.SetSpecService(b.specSvc)
		if b.blobStore != nil {
			loop.SetBlobStore(b.blobStore, 4000)
		}
		if b.subRunner != nil {
			loop.SetSubRunner(b.subRunner)
		}
		runner = loop
		orch = "native-offline"
		log.Printf("[bootstrap] orchestrator=native-offline (mock/no API key; Guard still on all tools)\n")
	}

	b.runner = runner
	b.orch = orch
}

// wireChat assembles the ChatApp with all options and the auth/token/device
// services, rehydrates HITL pendings, registers slash commands, and returns the
// fully wired App.
func (b *builder) wireChat() *App {
	cfg := b.cfg

	var chatOpts []application.Option
	chatOpts = append(chatOpts,
		application.WithSkills(b.skillSvc),
		application.WithMemory(b.memSvc),
		application.WithAudit(b.repos.AuditRepo),
		application.WithKeyStore(b.keyStore),
		application.WithCheckpoint(b.ckStore, b.runReg),
		application.WithSummaryRepo(b.repos.SummaryRepo),
	)
	if b.blobStore != nil {
		chatOpts = append(chatOpts, application.WithBlobStore(b.blobStore))
	}
	if b.mcpFactory != nil {
		chatOpts = append(chatOpts, application.WithMCPFactory(b.mcpFactory))
	}
	if b.sshPool != nil {
		chatOpts = append(chatOpts, application.WithSSH(b.sshPool, b.sshRepo))
	}
	chat := application.New(application.CoreDeps{
		Loop: b.runner, Sessions: b.repos.SessionRepo, Messages: b.repos.MessageRepo, Tools: b.reg, Perm: b.perm,
		Redis: b.rdb, TimeoutSec: cfg.Agent.TimeoutSec, Workspace: b.workspaceRoot,
		RateEnabled: cfg.RateLimit.Enabled, RatePerMin: cfg.RateLimit.PerMinute,
		QuotaEnabled: cfg.TokenQuota.Enabled, QuotaPerDay: cfg.TokenQuota.PerUserPerDay,
	}, chatOpts...)
	// per-step checkpoint snapshots (crash/restart resume)
	chat.SetHooks(b.hooks)

	// auth service (Sprint 1.2): signup, email verification, and credential
	// auth. JWT issuance arrives in Sprint 1.3.
	chat.SetAuthService(application.NewAuthService(b.repos.UserRepo, nil))

	// token service (Sprint 1.3): HS256 access tokens + rotating refresh tokens.
	chat.SetTokenService(application.NewTokenService(b.repos.UserRepo, b.repos.RefreshRepo, []byte(cfg.JWTSecret), []byte(cfg.JWTSecretPrev)))

	// device authorization service (Sprint 1.4): RFC8628 device flow for the TUI.
	chat.SetDeviceService(application.NewDeviceService(
		b.repos.DeviceRepo, b.repos.UserRepo, chat.TokenService(),
		cfg.Auth.VerificationURI,
		time.Duration(cfg.Auth.DeviceCodeTTLSec)*time.Second,
		time.Duration(cfg.Auth.DevicePollIntervalSec)*time.Second,
	))

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
	for _, t := range b.reg.List() {
		if t != nil && strings.Contains(t.Name(), "__") {
			mcpN++
		}
	}
	log.Printf("[bootstrap] db=%s tools=%d (mcp=%d) redis=%v mock_llm=%v workspace=%s subagent=%v orchestrator=%s\n",
		cfg.Database.Type, len(b.reg.List()), mcpN, b.rdb.Enabled(), cfg.LLM.UseMock, cfg.Agent.WorkspaceRoot, b.subRunner != nil, b.orch)

	var sshTermHub *wshub.SSHTerminalHub
	if b.sshPool != nil {
		sshTermHub = wshub.NewSSHTerminalHub(sshinfra.NewTerminal(b.sshPool), b.keyStore.Valid)
	}

	return &App{
		Config: cfg, Chat: chat, Tools: b.reg, Perm: b.perm, Redis: b.rdb,
		MCP: b.mcpFactory, MCPHealth: b.mcpHealth, Skills: b.skillSvc, Memory: b.memSvc, Hooks: b.hooks,
		Blobs: b.blobStore, Index: b.codeIdx, CKStore: b.ckStore, Runs: b.runReg,
		Host: b.hostExec, Bridge: b.hostBridge, HostHub: b.hostHub,
		SSHTerminalHub: sshTermHub,
		SSHPool:        b.sshPool,
		UserRepo:       b.repos.UserRepo,
		DeviceRepo:     b.repos.DeviceRepo,
		RefreshRepo:    b.repos.RefreshRepo,
		KMS:            b.sealer,
		LLMKey:         b.repos.LLMKeyRepo,
		Closer: func() {
			if b.mcpHealth != nil {
				b.mcpHealth.Stop()
			}
			if b.mcpFactory != nil {
				b.mcpFactory.ResetAll()
			}
			if b.sshPool != nil {
				b.sshPool.CloseAll()
			}
			_ = b.rdb.Close()
		},
	}
}
