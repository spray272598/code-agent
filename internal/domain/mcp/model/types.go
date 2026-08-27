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
	// OAuth configuration for MCP servers that require authentication.
	OAuth *OAuthConfig `json:"oauth,omitempty"`
	// Bearer token for simple token-based auth.
	BearerToken string `json:"bearerToken,omitempty"`
}

// OAuthConfig MCP server OAuth 2.0 configuration.
type OAuthConfig struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	AuthURL      string `json:"authUrl"`
	TokenURL     string `json:"tokenUrl"`
	RedirectURI  string `json:"redirectUri"`
	Scopes       string `json:"scopes"`
	UsePKCE      bool   `json:"usePkce"`
	Provider     string `json:"provider"`
}

// ToolDef MCP tool definition.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
	ServerName  string
}

// ResourceDef MCP resource definition.
type ResourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	ServerName  string `json:"-"`
}

// ResourceContent MCP resource content.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// PromptDef MCP prompt definition.
type PromptDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Arguments   []PromptArgDef `json:"arguments,omitempty"`
	ServerName  string         `json:"-"`
}

// PromptArgDef MCP prompt argument definition.
type PromptArgDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage MCP prompt message.
type PromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
