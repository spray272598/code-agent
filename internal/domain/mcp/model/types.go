package model

// ServerConfig MCP server configuration (domain value).
type ServerConfig struct {
	Name       string
	Transport  string // stdio | sse | http
	Command    string
	Args       []string
	Env        map[string]string
	URL        string
	Enabled    bool
	TimeoutSec int
}

// ToolDef MCP tool definition.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
	ServerName  string
}

// HealthStatus per-server health.
type HealthStatus struct {
	Name      string `json:"name"`
	Online    bool   `json:"online"`
	Transport string `json:"transport,omitempty"`
	ToolCount int    `json:"toolCount"`
	LastError string `json:"lastError,omitempty"`
	Enabled   bool   `json:"enabled"`
	// State is the connection circuit-breaker state: "normal", "retry",
	// "open", or "half_open". Empty implies normal.
	State string `json:"state,omitempty"`
}
