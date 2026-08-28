package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/spray272598/code-agent/internal/domain/model"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Agent      AgentConfig      `yaml:"agent"`
	LLM        LLMConfig        `yaml:"llm"`
	Database   DatabaseConfig   `yaml:"database"`
	Redis      RedisConfig      `yaml:"redis"`
	RateLimit  RateLimitConfig  `yaml:"rate_limit"`
	TokenQuota TokenQuotaConfig `yaml:"token_quota"`
	Storage    StorageConfig    `yaml:"storage"`
	MQ         MQConfig         `yaml:"mq"`
	Security   SecurityConfig   `yaml:"security"`
	MCP        MCPConfig        `yaml:"mcp"`
	Skills     SkillsConfig     `yaml:"skills"`
	Hooks      HooksConfig      `yaml:"hooks"`
	Commands   CommandsConfig   `yaml:"commands"`
	Logging    LoggingConfig    `yaml:"logging"`
	Teams      TeamsConfig      `yaml:"teams"`
	SubAgent   SubAgentConfig   `yaml:"subagent"`
	Host       HostConfig       `yaml:"host"`
	OTLP       OTLPConfig       `yaml:"otlp"`
	SSH        SSHConfig        `yaml:"ssh"`
	// Plugins configures the plugin system.
	Plugins PluginsConfig `yaml:"plugins"`
	// Vector selects the dense-vector backend for memory search + code RAG.
	// provider: "mem" (default, in-process) | "qdrant" (remote). Qdrant is only
	// used when embedding is also enabled (LLM.EmbeddingEnabled).
	Vector VectorConfig `yaml:"vector"`
	// JWTSecret signs platform access/refresh tokens (HS256). Rotate by moving the
	// current value to JWTSecretPrev and setting a new JWTSecret; tokens signed with
	// either secret remain valid during the overlap window.
	JWTSecret     string `yaml:"jwt_secret"`
	JWTSecretPrev string `yaml:"jwt_secret_prev"`
	// Auth holds RFC8628 device-flow / web-approval settings (Sprint 1.4).
	Auth AuthConfig `yaml:"auth"`
}

// AuthConfig device authorization (RFC8628) settings.
type AuthConfig struct {
	// VerificationURI is where the user enters the user_code in a browser
	// (e.g. https://app.example.com/devices/verify).
	VerificationURI string `yaml:"verification_uri"`
	// DeviceCodeTTLSec is how long a device_code/user_code stays valid.
	DeviceCodeTTLSec int `yaml:"device_code_ttl_sec"`
	// DevicePollIntervalSec is the minimum seconds between device token polls.
	DevicePollIntervalSec int `yaml:"device_poll_interval_sec"`
	// UserCodeLen is the length (before formatting) of the human user_code.
	UserCodeLen int `yaml:"user_code_len"`
}

// SSHConfig SSH remote operations toggle.
type SSHConfig struct {
	Enabled bool `yaml:"enabled"`
}

// VectorConfig selects the dense-vector backend for memory search and code RAG.
// provider: "mem" (default, in-process) | "qdrant" (remote). Qdrant is only
// used when embedding is also enabled (LLM.EmbeddingEnabled); otherwise the
// in-process MemIndex is always used regardless of this setting.
type VectorConfig struct {
	// Provider is "mem" or "qdrant".
	Provider string `yaml:"provider"`
	// QdrantURL is the Qdrant root, e.g. http://localhost:6333.
	QdrantURL string `yaml:"qdrant_url"`
	// QdrantAPIKey is the api-key header value; empty for unsecured instances.
	QdrantAPIKey string `yaml:"qdrant_api_key"`
	// Collection is the base collection name (default "codeagent"). Memory uses
	// the "memories" collection; code RAG uses "code".
	Collection string `yaml:"collection"`
	// Dimension is an optional explicit embedding dimension. 0 means derive
	// from the configured embedder.
	Dimension int `yaml:"dimension"`
}

type ServerConfig struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Mode    string `yaml:"mode"`
	TLSCert string `yaml:"tls_cert"` // PEM cert path; if set with tls_key → HTTPS
	TLSKey  string `yaml:"tls_key"`
}

type AgentConfig struct {
	Name          string `yaml:"name"`
	MaxSteps      int    `yaml:"max_steps"`
	TimeoutSec    int    `yaml:"timeout_sec"`
	TokenBudget   int    `yaml:"token_budget"`
	WorkspaceRoot string `yaml:"workspace_root"`
	// Orchestrator: "native" (default, self-built Loop) | "eino" (CloudWeGo Eino ReAct)
	Orchestrator string `yaml:"orchestrator"`
	// EinoStream enables streaming text_delta from Eino (orchestrator=eino only)
	EinoStream bool `yaml:"eino_stream"`
	// EinoGraphResume enables CheckPointStore + ResumeWithData for in-graph HITL (eino only).
	// Default true when orchestrator=eino.
	EinoGraphResume *bool `yaml:"eino_graph_resume"`
	// EinoCheckPointDir durable graph checkpoint dir (default ./data/eino-checkpoints)
	EinoCheckPointDir string `yaml:"eino_checkpoint_dir"`
	// CompactThresholdRatio warns/predictively pre-compacts at this window
	// occupancy ratio (0,1]. Default 0.8 → background summarize at 80% of budget.
	CompactThresholdRatio float64 `yaml:"compact_threshold_ratio"`
}

type LLMConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
	APIBase  string `yaml:"api_base"`
	UseMock  bool   `yaml:"use_mock"`
	// Embedding (semantic memory search). Empty model → embedding disabled.
	EmbeddingModel   string `yaml:"embedding_model"`
	EmbeddingAPIBase string `yaml:"embedding_api_base"`
	// EmbeddingEnabled is derived: true when EmbeddingModel != "" && APIKey != "".
	EmbeddingEnabled bool `yaml:"-"`
	// Routes enables M3/3.1 multi-model routing: map intent type to a
	// specific model endpoint. Empty → single-model (default route from above).
	Routes []ModelRouteConfig `yaml:"routes"`
}

// ModelRouteConfig is the YAML/Env representation of a model route.
type ModelRouteConfig struct {
	MatchIntent  string  `yaml:"match_intent"` // normal | deep | team | default
	Provider     string  `yaml:"provider"`
	Model        string  `yaml:"model"`
	APIBase      string  `yaml:"api_base"`
	APIKey       string  `yaml:"api_key"`
	CostPer1kIn  float64 `yaml:"cost_per_1k_in"`
	CostPer1kOut float64 `yaml:"cost_per_1k_out"`
}

// ToRoutes converts the LLM config into a model.Router. When no explicit
// routes are configured, a single default route is synthesized from the main
// LLM fields — preserving the historical single-model behavior.
func (l LLMConfig) ToRoutes() *model.Router {
	if len(l.Routes) == 0 {
		r := model.NewRouter([]model.ModelRoute{{
			MatchIntent:  "default",
			Provider:     l.Provider,
			Model:        l.Model,
			APIBase:      l.APIBase,
			APIKey:       l.APIKey,
			CostPer1kIn:  0,
			CostPer1kOut: 0,
		}})
		return r
	}
	routes := make([]model.ModelRoute, 0, len(l.Routes))
	for _, rc := range l.Routes {
		// Inherit missing credentials from the main LLM block so a route can
		// override only the model while reusing the shared key/base.
		apiKey, apiBase := rc.APIKey, rc.APIBase
		if apiKey == "" {
			apiKey = l.APIKey
		}
		if apiBase == "" {
			apiBase = l.APIBase
		}
		routes = append(routes, model.ModelRoute{
			MatchIntent:  rc.MatchIntent,
			Provider:     rc.Provider,
			Model:        rc.Model,
			APIBase:      apiBase,
			APIKey:       apiKey,
			CostPer1kIn:  rc.CostPer1kIn,
			CostPer1kOut: rc.CostPer1kOut,
		})
	}
	return model.NewRouter(routes)
}

type DatabaseConfig struct {
	Type        string      `yaml:"type"` // mysql | sqlite | memory
	AutoMigrate bool        `yaml:"auto_migrate"`
	SchemaPath  string      `yaml:"schema_path"`
	MySQL       MySQLConfig `yaml:"mysql"`
	// SQLitePath file path when type=sqlite (default ./data/code-agent.db)
	SQLitePath string `yaml:"sqlite_path"`
}

type MySQLConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type RateLimitConfig struct {
	Enabled   bool `yaml:"enabled"`
	PerMinute int  `yaml:"per_minute"`
}

type TokenQuotaConfig struct {
	Enabled       bool `yaml:"enabled"`
	PerUserPerDay int  `yaml:"per_user_per_day"`
}

type StorageConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Endpoint         string `yaml:"endpoint"`
	Bucket           string `yaml:"bucket"`
	Region           string `yaml:"region"`
	AccessKey        string `yaml:"access_key"`
	SecretKey        string `yaml:"secret_key"`
	UsePathStyle     bool   `yaml:"use_path_style"`
	LocalFallbackDir string `yaml:"local_fallback_dir"`
}

type MQConfig struct {
	Enabled bool   `yaml:"enabled"`
	Driver  string `yaml:"driver"`
	Stream  string `yaml:"stream"`
}

type SecurityConfig struct {
	APIKeys             []string `yaml:"api_keys"`
	PathSandbox         bool     `yaml:"path_sandbox"`
	DefaultConfirmWrite bool     `yaml:"default_confirm_write"`
	// SandboxMode selects the enforcement tier: "workspace" (default),
	// "readonly", or "strict". Mirrors Grok Build's kernel-enforced sandbox.
	SandboxMode string `yaml:"sandbox_mode"`
	// CORSOrigins allowlist; empty = same-origin only (no ACAO). Use ["*"] only for local demos.
	CORSOrigins []string `yaml:"cors_origins"`
	// MaxBodyBytes request body limit (default 2MiB)
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
}

type MCPConfig struct {
	Enabled bool `yaml:"enabled"`
	// ConfigFile is the path to an mcp.json (VS Code / Claude Desktop style
	// {"mcpServers": {...}}) file loaded at startup. Empty = none.
	ConfigFile string `yaml:"config_file"`
	// HotReload enables watching ConfigFile for changes and auto-reconnect.
	HotReload bool `yaml:"hot_reload"`
}

type SkillsConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Dir       string `yaml:"dir"`        // installed/local skills root
	MarketDir string `yaml:"market_dir"` // marketplace catalog root (browse-only)
	// RemoteURL is the base URL of a remote skill registry (e.g. https://skills.example.com).
	// When set, the remote marketplace is used alongside the local market.
	RemoteURL string `yaml:"remote_url"`
	// PublicKeyPath is the path to an Ed25519 public key (hex) for verifying
	// skill signatures from the remote registry. Empty = skip verification.
	PublicKeyPath string `yaml:"public_key_path"`
}

type HooksConfig struct {
	Enabled bool   `yaml:"enabled"`
	Dir     string `yaml:"dir"`
}

type CommandsConfig struct {
	Dir string `yaml:"dir"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type TeamsConfig struct {
	Enabled bool   `yaml:"enabled"`
	File    string `yaml:"file"` // teams/default.yaml
}

// PluginsConfig configures the plugin system.
type PluginsConfig struct {
	// Enabled enables the plugin system.
	Enabled bool `yaml:"enabled"`
	// Directory is the directory to search for plugins (manifest-based).
	Directory string `yaml:"directory"`
	// SODir is the directory to search for .so dynamic plugins.
	// Go plugins (.so) are loaded via plugin.Open at runtime.
	SODir string `yaml:"so_dir"`
}

type SubAgentConfig struct {
	Enabled       bool `yaml:"enabled"`
	MaxConcurrent int  `yaml:"max_concurrent"`
	DefaultSteps  int  `yaml:"default_steps"`
}

// HostConfig tool execution location.
// mode=server: tools on server workspace
// mode=host: prefer connected host-agent WebSocket (fallback local)
type HostConfig struct {
	Mode     string `yaml:"mode"` // server | host
	Endpoint string `yaml:"endpoint"`
	// PreferHost when true and a host is online, route coding tools to host
	PreferHost bool `yaml:"prefer_host"`
}

// OTLPConfig OpenTelemetry export.
type OTLPConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"` // host:port for OTLP HTTP, e.g. localhost:4318
	Insecure bool   `yaml:"insecure"`
	Service  string `yaml:"service"`
	// MetricsEnabled enables OTel Metrics API export (replaces custom Prometheus).
	MetricsEnabled bool `yaml:"metrics_enabled"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{Host: "0.0.0.0", Port: 8080, Mode: "debug"},
		Agent: AgentConfig{
			Name: "Code-Agent", MaxSteps: 20, TimeoutSec: 180,
			TokenBudget: 32000, WorkspaceRoot: "./workspace",
			// Primary: CloudWeGo Eino ReAct. Falls back to native when mock/no API key.
			Orchestrator:          "eino",
			EinoStream:            false,
			CompactThresholdRatio: 0.8,
		},
		LLM: LLMConfig{UseMock: true, Model: "deepseek-ai/DeepSeek-V3"},
		Database: DatabaseConfig{
			Type: "mysql", AutoMigrate: true, SchemaPath: "scripts/sql/01_schema.sql",
			MySQL: MySQLConfig{Host: "127.0.0.1", Port: 3306, Database: "code_agent", Username: "root", Password: "123456"},
		},
		Redis:      RedisConfig{Enabled: true, Host: "127.0.0.1", Port: 6379},
		RateLimit:  RateLimitConfig{Enabled: true, PerMinute: 60},
		TokenQuota: TokenQuotaConfig{Enabled: true, PerUserPerDay: 2000000},
		Storage: StorageConfig{
			Enabled: true, Endpoint: "http://127.0.0.1:9000", Bucket: "code-agent",
			AccessKey: "minioadmin", SecretKey: "minioadmin", UsePathStyle: true,
			LocalFallbackDir: "./data/objects",
		},
		Security: SecurityConfig{
			PathSandbox: true, DefaultConfirmWrite: true, APIKeys: []string{"dev-key"},
			CORSOrigins:  []string{"http://localhost:3000", "http://127.0.0.1:3000", "http://localhost:8080", "http://127.0.0.1:8080"},
			MaxBodyBytes: 2 << 20,
		},
		MCP:      MCPConfig{Enabled: true},
		Skills:   SkillsConfig{Enabled: true, Dir: "./skills", MarketDir: "./skills/market"},
		Hooks:    HooksConfig{Enabled: true, Dir: "./hooks"},
		Commands: CommandsConfig{Dir: "./commands"},
		Logging:  LoggingConfig{Level: "info"},
		Teams:    TeamsConfig{Enabled: true, File: "./teams/default.yaml"},
		SubAgent: SubAgentConfig{Enabled: true, MaxConcurrent: 3, DefaultSteps: 8},
		Host:     HostConfig{Mode: "server", PreferHost: false},
		Vector:   VectorConfig{Provider: "mem", Collection: "codeagent"},
		OTLP:     OTLPConfig{Enabled: false, Endpoint: "localhost:4318", Insecure: true, Service: "code-agent"},
		Auth: AuthConfig{
			VerificationURI:       "http://localhost:3000/devices/verify",
			DeviceCodeTTLSec:      300,
			DevicePollIntervalSec: 5,
			UserCodeLen:           8,
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config: %w", err)
			}
		} else if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(cfg)
	normalize(cfg)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("LLM_API_BASE"); v != "" {
		cfg.LLM.APIBase = v
	}
	// alias used by many Python projects
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.LLM.APIBase = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}
	if v := os.Getenv("LLM_USE_MOCK"); v != "" {
		cfg.LLM.UseMock = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("EMBEDDING_MODEL"); v != "" {
		cfg.LLM.EmbeddingModel = v
	}
	if v := os.Getenv("EMBEDDING_API_BASE"); v != "" {
		cfg.LLM.EmbeddingAPIBase = v
	}
	if v := os.Getenv("CODE_AGENT_API_KEY"); v != "" {
		cfg.Security.APIKeys = []string{v}
	}
	if v := os.Getenv("MYSQL_HOST"); v != "" {
		cfg.Database.MySQL.Host = v
	}
	if v := os.Getenv("MYSQL_PASSWORD"); v != "" {
		cfg.Database.MySQL.Password = v
	}
	if v := os.Getenv("DB_TYPE"); v != "" {
		cfg.Database.Type = v
	}
	if v := os.Getenv("REDIS_ENABLED"); v != "" {
		cfg.Redis.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WORKSPACE_ROOT"); v != "" {
		cfg.Agent.WorkspaceRoot = v
	}
	if v := os.Getenv("AGENT_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Agent.TimeoutSec = n
		}
	}
	if v := os.Getenv("AGENT_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Agent.MaxSteps = n
		}
	}
	if v := os.Getenv("AGENT_ORCHESTRATOR"); v != "" {
		cfg.Agent.Orchestrator = v
	}
	if v := os.Getenv("OTLP_ENABLED"); v != "" {
		cfg.OTLP.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("OTLP_ENDPOINT"); v != "" {
		cfg.OTLP.Endpoint = v
	}
	if v := os.Getenv("HOST_PREFER"); v != "" {
		cfg.Host.PreferHost = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("MINIO_ENDPOINT"); v != "" {
		cfg.Storage.Endpoint = v
	}
	if v := os.Getenv("VECTOR_PROVIDER"); v != "" {
		cfg.Vector.Provider = v
	}
	if v := os.Getenv("QDRANT_URL"); v != "" {
		cfg.Vector.QdrantURL = v
	}
	if v := os.Getenv("QDRANT_API_KEY"); v != "" {
		cfg.Vector.QdrantAPIKey = v
	}
	if v := os.Getenv("QDRANT_COLLECTION"); v != "" {
		cfg.Vector.Collection = v
	}
	if v := os.Getenv("VECTOR_DIMENSION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Vector.Dimension = n
		}
	}
	// empty key → mock for local dev
	if cfg.LLM.APIKey == "" {
		cfg.LLM.UseMock = true
	}
	// embedding enabled only when a model is configured and a real key exists
	cfg.LLM.EmbeddingEnabled = cfg.LLM.EmbeddingModel != "" && cfg.LLM.APIKey != "" && !cfg.LLM.UseMock
}

func normalize(cfg *Config) {
	if cfg.Server.Port <= 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Agent.MaxSteps <= 0 {
		cfg.Agent.MaxSteps = 20
	}
	if cfg.Agent.TokenBudget <= 0 {
		cfg.Agent.TokenBudget = 32000
	}
	if cfg.Agent.TimeoutSec <= 0 {
		cfg.Agent.TimeoutSec = 180
	}
	if cfg.Agent.WorkspaceRoot == "" {
		cfg.Agent.WorkspaceRoot = "./workspace"
	}
	if strings.TrimSpace(cfg.Agent.Orchestrator) == "" {
		cfg.Agent.Orchestrator = "eino"
	}
	if cfg.Agent.EinoCheckPointDir == "" {
		cfg.Agent.EinoCheckPointDir = "./data/eino-checkpoints"
	}
	if cfg.RateLimit.PerMinute <= 0 {
		cfg.RateLimit.PerMinute = 60
	}
	if cfg.Database.MySQL.Port <= 0 {
		cfg.Database.MySQL.Port = 3306
	}
	if cfg.Redis.Port <= 0 {
		cfg.Redis.Port = 6379
	}
	if len(cfg.Security.APIKeys) == 0 {
		cfg.Security.APIKeys = []string{"dev-key"}
	}
	if cfg.Security.MaxBodyBytes <= 0 {
		cfg.Security.MaxBodyBytes = 2 << 20
	}
}

// Validate checks configuration values and returns an error for invalid settings.
// This should be called after normalize() to catch explicit invalid values.
func Validate(cfg *Config) error {
	if cfg.Server.Port < 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 0 and 65535, got %d", cfg.Server.Port)
	}
	if cfg.Agent.MaxSteps <= 0 || cfg.Agent.MaxSteps > 1000 {
		return fmt.Errorf("agent.max_steps must be between 1 and 1000, got %d", cfg.Agent.MaxSteps)
	}
	if cfg.Agent.TokenBudget <= 0 || cfg.Agent.TokenBudget > 1000000 {
		return fmt.Errorf("agent.token_budget must be between 1 and 1000000, got %d", cfg.Agent.TokenBudget)
	}
	if cfg.Agent.TimeoutSec <= 0 || cfg.Agent.TimeoutSec > 3600 {
		return fmt.Errorf("agent.timeout_sec must be between 1 and 3600, got %d", cfg.Agent.TimeoutSec)
	}
	if cfg.RateLimit.PerMinute <= 0 || cfg.RateLimit.PerMinute > 10000 {
		return fmt.Errorf("rate_limit.per_minute must be between 1 and 10000, got %d", cfg.RateLimit.PerMinute)
	}
	if cfg.Database.MySQL.Port < 0 || cfg.Database.MySQL.Port > 65535 {
		return fmt.Errorf("database.mysql.port must be between 0 and 65535, got %d", cfg.Database.MySQL.Port)
	}
	if cfg.Redis.Port < 0 || cfg.Redis.Port > 65535 {
		return fmt.Errorf("redis.port must be between 0 and 65535, got %d", cfg.Redis.Port)
	}
	if cfg.Security.MaxBodyBytes <= 0 {
		return fmt.Errorf("security.max_body_bytes must be positive, got %d", cfg.Security.MaxBodyBytes)
	}
	return nil
}

// EinoGraphResumeEnabled returns whether graph-level interrupt resume is on (default true).
func (c *Config) EinoGraphResumeEnabled() bool {
	if c == nil {
		return true
	}
	if c.Agent.EinoGraphResume == nil {
		return true
	}
	return *c.Agent.EinoGraphResume
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) MySQLDSN() string {
	m := c.Database.MySQL
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&loc=Local",
		m.Username, m.Password, m.Host, m.Port, m.Database)
}
