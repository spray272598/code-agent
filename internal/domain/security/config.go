package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type SecurityConfig struct {
	Profiles map[string]ProfileConfig `yaml:"profiles" json:"profiles"`
	Deny     DenyConfig               `yaml:"deny" json:"deny"`
	Network  NetworkPolicyConfig      `yaml:"network" json:"network"`
	Audit    AuditConfig              `yaml:"audit" json:"audit"`
	Isolate  IsolationConfig          `yaml:"isolate" json:"isolate"`
}

type ProfileConfig struct {
	Name         string   `yaml:"name" json:"name"`
	ReadOnly     []string `yaml:"read_only" json:"readOnly"`
	ReadWrite    []string `yaml:"read_write" json:"readWrite"`
	Deny         []string `yaml:"deny" json:"deny"`
	NetworkBlock bool     `yaml:"network_block" json:"networkBlock"`
	MaxSteps     int      `yaml:"max_steps" json:"maxSteps"`
	Extends      string   `yaml:"extends" json:"extends"`
	Description  string   `yaml:"description" json:"description"`
}

type NetworkPolicyConfig struct {
	BlockAll   bool     `yaml:"block_all" json:"blockAll"`
	AllowSites []string `yaml:"allow_sites" json:"allowSites"`
	DenySites  []string `yaml:"deny_sites" json:"denySites"`
}

type AuditConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	LogPath      string `yaml:"log_path" json:"logPath"`
	MaxSizeMB    int    `yaml:"max_size_mb" json:"maxSizeMb"`
	FlushOnWrite bool   `yaml:"flush_on_write" json:"flushOnWrite"`
}

type IsolationConfig struct {
	GlobalPath   string `yaml:"global_path" json:"globalPath"`
	ProjectPath  string `yaml:"project_path" json:"projectPath"`
	PreferGlobal bool   `yaml:"prefer_global" json:"preferGlobal"`
}

type ConfigLoader struct {
	globalPath  string
	projectPath string
	mu          sync.RWMutex
	cached      *SecurityConfig
	loaded      bool
}

func NewConfigLoader(workspace string) *ConfigLoader {
	globalPath := filepath.Join(os.Getenv("USERPROFILE"), ".code-agent", "security.yaml")
	if os.Getenv("HOME") != "" {
		globalPath = filepath.Join(os.Getenv("HOME"), ".code-agent", "security.yaml")
	}
	projectPath := filepath.Join(workspace, ".code-agent", "security.yaml")
	return &ConfigLoader{
		globalPath:  globalPath,
		projectPath: projectPath,
	}
}

func (l *ConfigLoader) Load() (*SecurityConfig, error) {
	l.mu.RLock()
	if l.loaded && l.cached != nil {
		cfg := *l.cached
		l.mu.RUnlock()
		return &cfg, nil
	}
	l.mu.RUnlock()

	l.mu.Lock()
	defer l.mu.Unlock()

	cfg, err := l.loadInternal()
	if err != nil {
		return nil, err
	}
	l.cached = cfg
	l.loaded = true
	return cfg, nil
}

func (l *ConfigLoader) loadInternal() (*SecurityConfig, error) {
	globalCfg := &SecurityConfig{
		Profiles: defaultProfiles(),
		Deny:     DenyConfig{},
		Network:  NetworkPolicyConfig{},
		Audit: AuditConfig{
			Enabled:      true,
			LogPath:      "audit.log",
			MaxSizeMB:    100,
			FlushOnWrite: true,
		},
		Isolate: IsolationConfig{
			GlobalPath:   l.globalPath,
			ProjectPath:  l.projectPath,
			PreferGlobal: true,
		},
	}

	if err := loadYAMLFile(l.globalPath, globalCfg); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("global config: %w", err)
	}

	projectCfg := &SecurityConfig{}
	if err := loadYAMLFile(l.projectPath, projectCfg); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("project config: %w", err)
	}

	merged := mergeConfigs(globalCfg, projectCfg)
	merged.Isolate = globalCfg.Isolate

	merged.Profiles = resolveProfiles(merged.Profiles)
	merged.Deny = mergeDenyConfig(globalCfg.Deny, projectCfg.Deny)
	merged.Network = mergeNetworkConfig(globalCfg.Network, projectCfg.Network)

	return merged, nil
}

func (l *ConfigLoader) DetectConflicts() []string {
	var conflicts []string
	globalCfg := &SecurityConfig{Profiles: defaultProfiles()}
	_ = loadYAMLFile(l.globalPath, globalCfg)

	projectCfg := &SecurityConfig{}
	if err := loadYAMLFile(l.projectPath, projectCfg); err != nil && !os.IsNotExist(err) {
		return nil
	}

	for name := range projectCfg.Profiles {
		if _, exists := globalCfg.Profiles[name]; exists {
			if name == "workspace" || name == "readonly" || name == "strict" {
				conflicts = append(conflicts, fmt.Sprintf("profile %q conflicts with built-in/global profile", name))
			}
		}
	}
	return conflicts
}

func defaultProfiles() map[string]ProfileConfig {
	return map[string]ProfileConfig{
		"workspace": {
			Name:        "workspace",
			Description: "Default: full access within workspace, writes allowed",
			ReadWrite:   []string{"${workspace}"},
			Deny: []string{
				"**/.env", "**/*.pem", "**/secrets/**",
			},
			NetworkBlock: false,
			MaxSteps:     200,
		},
		"readonly": {
			Name:        "readonly",
			Description: "Read-only: denies all mutating tools",
			ReadWrite:   []string{},
			Deny: []string{
				"**/.env", "**/*.pem", "**/secrets/**",
			},
			NetworkBlock: false,
			MaxSteps:     100,
		},
		"strict": {
			Name:        "strict",
			Description: "Strict: network blocked, only workspace access",
			ReadWrite:   []string{"${workspace}"},
			Deny: []string{
				"**/.env", "**/*.pem", "**/secrets/**", "**/.ssh/**",
			},
			NetworkBlock: true,
			MaxSteps:     50,
		},
		"devbox": {
			Name:        "devbox",
			Description: "Devbox: full dev environment with network",
			ReadWrite:   []string{"${workspace}"},
			Deny: []string{
				"**/.env", "**/*.pem",
			},
			NetworkBlock: false,
			MaxSteps:     500,
		},
		"sandboxed": {
			Name:        "sandboxed",
			Description: "Sandboxed: minimal access, network blocked, step limit",
			ReadWrite:   []string{"${workspace}/src", "${workspace}/test"},
			Deny: []string{
				"**/.env", "**/*.pem", "**/secrets/**",
				"**/.ssh/**", "**/credentials*",
			},
			NetworkBlock: true,
			MaxSteps:     30,
		},
	}
}

func resolveProfiles(profiles map[string]ProfileConfig) map[string]ProfileConfig {
	resolved := make(map[string]ProfileConfig, len(profiles))
	for name, p := range profiles {
		resolved[name] = resolveProfile(p, profiles, 0)
	}
	return resolved
}

func resolveProfile(p ProfileConfig, profiles map[string]ProfileConfig, depth int) ProfileConfig {
	if depth > 10 || p.Extends == "" {
		return p
	}
	parent, ok := profiles[p.Extends]
	if !ok {
		return p
	}
	parent = resolveProfile(parent, profiles, depth+1)
	if len(p.ReadWrite) == 0 {
		p.ReadWrite = parent.ReadWrite
	}
	if len(p.ReadOnly) == 0 {
		p.ReadOnly = parent.ReadOnly
	}
	if len(p.Deny) == 0 {
		p.Deny = parent.Deny
	}
	if !p.NetworkBlock {
		p.NetworkBlock = parent.NetworkBlock
	}
	if p.MaxSteps <= 0 {
		p.MaxSteps = parent.MaxSteps
	}
	return p
}

func mergeConfigs(global, project *SecurityConfig) *SecurityConfig {
	merged := &SecurityConfig{
		Profiles: make(map[string]ProfileConfig),
		Deny:     global.Deny,
		Network:  global.Network,
		Audit:    global.Audit,
		Isolate:  global.Isolate,
	}

	for name, p := range global.Profiles {
		merged.Profiles[name] = p
	}
	for name, p := range project.Profiles {
		if _, exists := merged.Profiles[name]; exists {
			if p.Extends == "" || p.Extends == name {
				merged.Profiles[name] = p
			} else {
				merged.Profiles[name] = p
			}
		} else {
			merged.Profiles[name] = p
		}
	}

	return merged
}

func mergeDenyConfig(global, project DenyConfig) DenyConfig {
	merged := DenyConfig{}
	seen := make(map[string]bool)
	for _, p := range global.ExactPaths {
		if !seen[p] {
			seen[p] = true
			merged.ExactPaths = append(merged.ExactPaths, p)
		}
	}
	for _, p := range project.ExactPaths {
		if !seen[p] {
			seen[p] = true
			merged.ExactPaths = append(merged.ExactPaths, p)
		}
	}
	seenGlob := make(map[string]bool)
	for _, p := range global.GlobPatterns {
		if !seenGlob[p] {
			seenGlob[p] = true
			merged.GlobPatterns = append(merged.GlobPatterns, p)
		}
	}
	for _, p := range project.GlobPatterns {
		if !seenGlob[p] {
			seenGlob[p] = true
			merged.GlobPatterns = append(merged.GlobPatterns, p)
		}
	}
	return merged
}

func mergeNetworkConfig(global, project NetworkPolicyConfig) NetworkPolicyConfig {
	merged := NetworkPolicyConfig{}
	if global.BlockAll {
		merged.BlockAll = true
	} else if project.BlockAll {
		merged.BlockAll = true
	}
	seen := make(map[string]bool)
	for _, s := range global.AllowSites {
		if !seen[s] {
			seen[s] = true
			merged.AllowSites = append(merged.AllowSites, s)
		}
	}
	for _, s := range project.AllowSites {
		if !seen[s] {
			seen[s] = true
			merged.AllowSites = append(merged.AllowSites, s)
		}
	}
	seenDeny := make(map[string]bool)
	for _, s := range global.DenySites {
		if !seenDeny[s] {
			seenDeny[s] = true
			merged.DenySites = append(merged.DenySites, s)
		}
	}
	for _, s := range project.DenySites {
		if !seenDeny[s] {
			seenDeny[s] = true
			merged.DenySites = append(merged.DenySites, s)
		}
	}
	return merged
}

func loadYAMLFile(path string, cfg *SecurityConfig) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return parseYAML(data, cfg)
}

func parseYAML(data []byte, cfg *SecurityConfig) error {
	content := string(data)
	lines := strings.Split(content, "\n")
	var currentSection string
	var currentProfileName string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		cleaned := strings.TrimSpace(trimmed)

		switch {
		case indent == 0 && strings.HasPrefix(cleaned, "profiles:"):
			currentSection = "profiles"
		case indent == 0 && strings.HasPrefix(cleaned, "deny:"):
			currentSection = "deny"
		case indent == 0 && strings.HasPrefix(cleaned, "network:"):
			currentSection = "network"
		case indent == 0 && strings.HasPrefix(cleaned, "audit:"):
			currentSection = "audit"
		case indent == 0 && strings.HasPrefix(cleaned, "isolate:"):
			currentSection = "isolate"
		case currentSection == "profiles" && indent == 2 && strings.HasSuffix(cleaned, ":"):
			name := strings.TrimSuffix(cleaned, ":")
			cfg.Profiles[name] = ProfileConfig{Name: name}
			currentProfileName = name
		case currentProfileName != "" && indent >= 4:
			kv := strings.SplitN(cleaned, ":", 2)
			if len(kv) == 2 {
				key := strings.TrimSpace(kv[0])
				val := strings.TrimSpace(kv[1])
				cp := cfg.Profiles[currentProfileName]
				switch key {
				case "name":
					cp.Name = unquote(val)
				case "description":
					cp.Description = unquote(val)
				case "extends":
					cp.Extends = unquote(val)
				case "network_block":
					cp.NetworkBlock = val == "true" || val == "yes"
				case "max_steps":
					fmt.Sscanf(val, "%d", &cp.MaxSteps)
				}
				cfg.Profiles[currentProfileName] = cp
			}
		}
	}
	return nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func (l *ConfigLoader) GetProfile(name string) (ProfileConfig, bool) {
	cfg, err := l.Load()
	if err != nil {
		return ProfileConfig{}, false
	}
	p, ok := cfg.Profiles[name]
	return p, ok
}

func (l *ConfigLoader) GetDenyEngine() (*DenyEngine, error) {
	cfg, err := l.Load()
	if err != nil {
		return nil, err
	}
	return NewDenyEngine(cfg.Deny)
}

func (l *ConfigLoader) InvalidateCache() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cached = nil
	l.loaded = false
}
