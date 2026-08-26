package port

import "context"

// ILLMPort is implemented in infrastructure/llm. Domain never imports infra.
type ILLMPort interface {
	Generate(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	GenerateStream(ctx context.Context, req *ChatRequest, onDelta func(delta StreamDelta)) (*ChatResponse, error)
}

type ChatRequest struct {
	SystemPrompt string
	Messages     []ChatMessage
	Temperature  float64
	MaxTokens    int
	// Tools optional for native function-calling providers
	Tools []ToolSpec
}

type ChatMessage struct {
	Role       string // user | assistant | tool | system
	Content    string
	Name       string
	ToolCallID string
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

type StreamDelta struct {
	Type     string // text | thought | tool_call_partial
	Text     string
	ToolCall *ToolCall
}

type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	PromptTokens int
	OutputTokens int
	TotalTokens  int
	Raw          string
}
