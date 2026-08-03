package team

import (
	"os"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/subagent"
	"gopkg.in/yaml.v3"
)

// Config maps role names to tool allowlists (thin Agent Teams).
type Config struct {
	Name  string                `yaml:"name"`
	Roles map[string]RoleConfig `yaml:"roles"`
}

type RoleConfig struct {
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
	MaxSteps    int      `yaml:"max_steps"`
}

// LoadYAML file; on failure returns nil.
func LoadYAML(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ApplyToRunner merges team roles into subagent runner.
func ApplyToRunner(r *subagent.Runner, c *Config) {
	if r == nil || c == nil || len(c.Roles) == 0 {
		return
	}
	if r.Roles == nil {
		r.Roles = map[string]subagent.RoleConfig{}
	}
	for name, rc := range c.Roles {
		key := strings.ToLower(name)
		r.Roles[key] = subagent.RoleConfig{
			Tools: append([]string{}, rc.Tools...),
			MaxSteps: rc.MaxSteps,
		}
	}
}

// ListRoles for slash/help.
func ListRoles(c *Config) string {
	if c == nil || len(c.Roles) == 0 {
		return "default roles: explore, verify, general"
	}
	var b strings.Builder
	b.WriteString("team: ")
	b.WriteString(c.Name)
	b.WriteString("\n")
	for name, rc := range c.Roles {
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(rc.Description)
		b.WriteString(" tools=")
		b.WriteString(strings.Join(rc.Tools, ","))
		b.WriteString("\n")
	}
	return b.String()
}
