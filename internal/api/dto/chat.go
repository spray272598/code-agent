// Package dto holds HTTP/API contracts at the edge (not domain).
// Aligns with walicode-server-api: interface types live outside domain.
package dto

// ChatRequest is the public chat payload.
type ChatRequest struct {
	SessionID      string `json:"sessionId"`
	UserID         string `json:"userId"`
	ProjectID      string `json:"projectId"`
	Message        string `json:"message"`
	AutoApprove    bool   `json:"autoApprove"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// ChatResponse is the public chat result.
type ChatResponse struct {
	SessionID      string `json:"sessionId"`
	Response       string `json:"response"`
	Steps          int    `json:"steps"`
	ToolCalls      int    `json:"toolCalls"`
	TokenUsed      int    `json:"tokenUsed"`
	NeedPermission bool   `json:"needPermission,omitempty"`
	Pending        any    `json:"pendingPermission,omitempty"`
	ErrorClass     string `json:"errorClass,omitempty"`
	Slash          bool   `json:"slash,omitempty"`
}

// MemorySaveRequest POST /api/v1/memory
type MemorySaveRequest struct {
	UserID     string `json:"userId"`
	ProjectID  string `json:"projectId"`
	Scope      string `json:"scope"`
	Category   string `json:"category"`
	Content    string `json:"content"`
	Importance int    `json:"importance"`
	Source     string `json:"source"`
}

// MCPInstallRequest POST /api/v1/mcp/servers
type MCPInstallRequest struct {
	Name       string            `json:"name"`
	Transport  string            `json:"transport"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	URL        string            `json:"url"`
	Enabled    *bool             `json:"enabled"`
	TimeoutSec int               `json:"timeoutSec"`
}
