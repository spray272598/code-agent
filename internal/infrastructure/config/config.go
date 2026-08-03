package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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
}

type LLMConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
	APIBase  string `yaml:"api_base"`
	UseMock  bool   `yaml:"use_mock"`
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
	Enabled        bool `yaml:"enabled"`
	PerUserPerDay  int  `yaml:"per_user_per_day"`
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
	// CORSOrigins allowlist; empty = same-origin only (no ACAO). Use ["*"] only for local demos.
	CORSOrigins []string `yaml:"cors_origins"`
	// MaxBodyBytes request body limit (default 2MiB)
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
}

type MCPConfig struct {
	Enabled bool `yaml:"enabled"`
}

type SkillsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Dir     string `yaml:"dir"`
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
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{Host: "0.0.0.0", Port: 8080, Mode: "debug"},
		Agent: AgentConfig{
			Name: "Code-Agent", MaxSteps: 20, TimeoutSec: 180,
			TokenBudget: 32000, WorkspaceRoot: "./workspace",
			// Primary: CloudWeGo Eino ReAct. Falls back to native when mock/no API key.
			Orchestrator: "eino",
			EinoStream:   false,
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
		Skills:   SkillsConfig{Enabled: true, Dir: "./skills"},
		Hooks:    HooksConfig{Enabled: true, Dir: "./hooks"},
		Commands: CommandsConfig{Dir: "./commands"},
		Logging:  LoggingConfig{Level: "info"},
		Teams:    TeamsConfig{Enabled: true, File: "./teams/default.yaml"},
		SubAgent: SubAgentConfig{Enabled: true, MaxConcurrent: 3, DefaultSteps: 8},
		Host: HostConfig{Mode: "server", PreferHost: false},
		OTLP: OTLPConfig{Enabled: false, Endpoint: "localhost:4318", Insecure: true, Service: "code-agent"},
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
	// empty key → mock for local dev
	if cfg.LLM.APIKey == "" {
		cfg.LLM.UseMock = true
	}
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

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) MySQLDSN() string {
	m := c.Database.MySQL
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&loc=Local",
		m.Username, m.Password, m.Host, m.Port, m.Database)
}
