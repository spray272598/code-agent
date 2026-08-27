package plugin

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// Capability represents the type of capability a plugin provides.
type Capability string

const (
	CapabilityTool     Capability = "tool"     // 工具能力
	CapabilityLLM      Capability = "llm"      // LLM适配器
	CapabilityStorage  Capability = "storage"  // 存储后端
	CapabilitySecurity Capability = "security" // 安全策略
	CapabilityUI       Capability = "ui"       // UI组件
)

// PluginContext provides a plugin with access to shared services and resources.
type PluginContext struct {
	// Tools is the global tool registry.
	Tools *tool.MapRegistry

	// LLM is the LLM adapter for making model calls.
	LLM LLMAdapter

	// Storage is the storage provider for persisting data.
	Storage StorageProvider

	// Security is the security policy checker.
	Security SecurityPolicy

	// Config is the configuration manager.
	Config ConfigManager

	// Logger is the plugin logger.
	Logger Logger

	// Events is the event bus for inter-plugin communication.
	Events EventBus

	// Sandbox is the sandbox interface for restricted execution.
	Sandbox Sandbox
}

// LLMAdapter is the interface for LLM adapters.
type LLMAdapter interface {
	// Generate generates a response from the LLM.
	Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error)

	// Stream streams a response from the LLM.
	Stream(ctx context.Context, req *LLMRequest) (<-chan *LLMChunk, error)
}

// LLMRequest represents a request to the LLM.
type LLMRequest struct {
	Messages    []LLMMessage `json:"messages"`
	Model       string       `json:"model,omitempty"`
	MaxTokens   int          `json:"maxTokens,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	Tools       []LLMTool    `json:"tools,omitempty"`
}

// LLMMessage represents a message in the LLM conversation.
type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LLMTool represents a tool available to the LLM.
type LLMTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// LLMResponse represents a response from the LLM.
type LLMResponse struct {
	Content   string       `json:"content"`
	ToolCalls []LLMToolCall `json:"toolCalls,omitempty"`
	Usage     *LLMUsage    `json:"usage,omitempty"`
}

// LLMToolCall represents a tool call from the LLM.
type LLMToolCall struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// LLMUsage represents token usage.
type LLMUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// LLMChunk represents a streaming chunk from the LLM.
type LLMChunk struct {
	Delta     string       `json:"delta,omitempty"`
	ToolCalls []LLMToolCall `json:"toolCalls,omitempty"`
	Usage     *LLMUsage    `json:"usage,omitempty"`
	Done      bool         `json:"done"`
}

// StorageProvider is the interface for storage backends.
type StorageProvider interface {
	// Get retrieves data by key.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores data with the given key.
	Set(ctx context.Context, key string, value []byte) error

	// Delete removes data by key.
	Delete(ctx context.Context, key string) error

	// List returns all keys with the given prefix.
	List(ctx context.Context, prefix string) ([]string, error)
}

// SecurityPolicy is the interface for security policy enforcement.
type SecurityPolicy interface {
	// CheckPermission checks if the given action is allowed.
	CheckPermission(ctx context.Context, action string, resource string) (bool, error)
}

// ConfigManager is the interface for configuration management.
type ConfigManager interface {
	// Get retrieves a configuration value.
	Get(key string) (any, error)

	// Set sets a configuration value.
	Set(key string, value any) error

	// Watch watches for configuration changes.
	Watch(key string, callback func(any)) error
}

// Logger is the interface for plugin logging.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// EventBus is the interface for inter-plugin communication.
type EventBus interface {
	// Emit emits an event.
	Emit(event string, data any) error

	// On registers a handler for an event.
	On(event string, handler func(any)) error

	// Off removes a handler for an event.
	Off(event string, handler func(any)) error
}

// Sandbox is the interface for restricted execution.
type Sandbox interface {
	// ExecuteInSandbox executes a command in the sandbox.
	ExecuteInSandbox(ctx context.Context, cmd string, args []string) ([]byte, error)
}

// ExtendedPlugin extends the base Plugin interface with richer capabilities.
type ExtendedPlugin interface {
	Plugin

	// ID returns the unique identifier for this plugin.
	ID() string

	// Version returns the plugin version.
	Version() string

	// Capabilities returns the capabilities this plugin provides.
	Capabilities() []Capability

	// Init initializes the plugin with the given context.
	Init(ctx *PluginContext) error
}
