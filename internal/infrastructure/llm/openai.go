package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
)

// Sentinel errors surfaced to the session/loop layer for special handling.
var (
	// ErrContextOverflow means the request exceeded the model's context window.
	// The caller should compress history and resubmit rather than retry blindly.
	ErrContextOverflow = errors.New("llm: context window overflow — needs compaction")
	// ErrAuth means the API key or credential is invalid (HTTP 401/403).
	ErrAuth = errors.New("llm: authentication failed")
)

type OpenAIGateway struct {
	apiKey     string
	apiBase    string
	model      string
	client     *http.Client
	maxRetries int
}

func NewOpenAI(apiKey, apiBase, model string) *OpenAIGateway {
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAIGateway{
		apiKey: apiKey, apiBase: apiBase, model: model,
		client:     &http.Client{Timeout: 180 * time.Second},
		maxRetries: MaxLLMRetries,
	}
}

func NewFromConfig(cfg *config.Config) port.ILLMPort {
	if cfg.LLM.UseMock || cfg.LLM.APIKey == "" {
		return NewMock()
	}
	return NewOpenAI(cfg.LLM.APIKey, cfg.LLM.APIBase, cfg.LLM.Model)
}

type chatMsg struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

func (g *OpenAIGateway) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	return g.doWithRetry(ctx, req, false, nil)
}

func (g *OpenAIGateway) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(port.StreamDelta)) (*port.ChatResponse, error) {
	return g.doWithRetry(ctx, req, true, onDelta)
}

// ContextWindow returns the configured model's context window in tokens.
// Implements the optional port.ModelWindowProvider interface so callers can
// bind token budgets to the real window instead of a hardcoded default.
func (g *OpenAIGateway) ContextWindow() int {
	return modelContextWindow(g.model)
}

// modelContextWindow maps a model id to its context window (tokens). Unknown
// models fall back to a conservative 128k window.
func modelContextWindow(model string) int {
	switch {
	case strings.Contains(model, "gpt-4o"):
		return 128000
	case strings.Contains(model, "gpt-4-turbo"):
		return 128000
	case strings.Contains(model, "gpt-4"):
		return 8192
	case strings.Contains(model, "gpt-3.5"):
		return 16385
	case strings.Contains(model, "grok"):
		return 131072
	case strings.Contains(model, "claude"):
		return 200000
	default:
		return 128000
	}
}

// doWithRetry wraps do() with the retry classifier loop. Transient errors
// (429/5xx) are retried with exponential backoff. Context overflow (400) returns
// ErrContextOverflow so the caller can compress and resubmit. Auth errors (401/403)
// return ErrAuth for credential refresh.
func (g *OpenAIGateway) doWithRetry(ctx context.Context, req *port.ChatRequest, stream bool, onDelta func(port.StreamDelta)) (*port.ChatResponse, error) {
	maxRetries := g.maxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := g.do(ctx, req, stream, onDelta)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Extract status code from error for classification.
		status := extractStatus(err)
		if status == 0 {
			// Not an HTTP error (e.g. connection refused, context cancelled) — don't retry.
			return nil, err
		}

		result := ClassifyLLMError(status, err.Error(), attempt, maxRetries)

		switch result.Decision {
		case RetryDecisionFatal:
			return nil, err

		case RetryDecisionEmitToSession:
			if status == 401 || status == 403 {
				return nil, ErrAuth
			}
			return nil, err

		case RetryDecisionRetryCompaction:
			return nil, ErrContextOverflow

		case RetryDecisionRetry, RetryDecisionRetryBackoff:
			if result.Backoff > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(result.Backoff):
				}
			}
			// Loop continues to next attempt
		}
	}

	return nil, fmt.Errorf("llm: %d retries exhausted: %w", maxRetries, lastErr)
}

func (g *OpenAIGateway) do(ctx context.Context, req *port.ChatRequest, stream bool, onDelta func(port.StreamDelta)) (*port.ChatResponse, error) {
	msgs := make([]chatMsg, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, chatMsg{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		role := m.Role
		if role == "" {
			role = "user"
		}
		cm := chatMsg{Role: role, Content: m.Content, Name: m.Name}
		if role == "tool" {
			cm.ToolCallID = m.ToolCallID
			if cm.ToolCallID == "" {
				cm.ToolCallID = "call_" + m.Name
			}
		}
		msgs = append(msgs, cm)
	}
	temp := req.Temperature
	if temp == 0 {
		temp = 0.2
	}
	body := map[string]any{
		"model": g.model, "messages": msgs, "temperature": temp, "stream": stream,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiBase+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(b))
	}
	if stream {
		return g.readStream(resp.Body, onDelta)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens, CompletionTokens, TotalTokens int
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	content := ""
	if len(out.Choices) > 0 {
		content = out.Choices[0].Message.Content
	}
	return &port.ChatResponse{
		Content:      content,
		PromptTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
		TotalTokens: out.Usage.TotalTokens,
	}, nil
}

func (g *OpenAIGateway) readStream(r io.Reader, onDelta func(port.StreamDelta)) (*port.ChatResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var b strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			t := chunk.Choices[0].Delta.Content
			if t != "" {
				b.WriteString(t)
				if onDelta != nil {
					onDelta(port.StreamDelta{Type: "text", Text: t})
				}
			}
		}
	}
	return &port.ChatResponse{Content: b.String(), TotalTokens: len(b.String()) / 4}, sc.Err()
}

// extractStatus pulls the HTTP status code from an error message produced by do().
// Returns 0 if the error is not an HTTP error.
func extractStatus(err error) int {
	msg := err.Error()
	if !strings.HasPrefix(msg, "llm http ") {
		return 0
	}
	// Format: "llm http 400: body" — parse digits, stop at first non-digit.
	rest := strings.TrimPrefix(msg, "llm http ")
	code := 0
	for _, c := range rest {
		if c >= '0' && c <= '9' {
			code = code*10 + int(c-'0')
		} else {
			break
		}
	}
	return code
}
