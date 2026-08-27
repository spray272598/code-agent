package llm

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// --- LLM Middleware ---
//
// Inspired by DeepSeek Harness's LlmRuntime pattern:
// Provider routing, retry, metrics, and logging are composable middleware
// rather than hardcoded in the runner.

// LLMCall represents a request to the LLM.
type LLMCall struct {
	Model    string
	Messages []port.ChatMessage
	Stream   bool
}

// LLMResponse is the result of an LLM call.
type LLMResponse struct {
	Content          string
	ToolCalls        []port.ToolCall
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// LLMFunc is the base function that makes an LLM call.
type LLMFunc func(ctx context.Context, call LLMCall) (*LLMResponse, error)

// LLMMiddleware wraps an LLMFunc with additional behavior.
type LLMMiddleware func(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error)

// ComposeLLM chains middleware around a base function.
// Usage:
//
//	base := adapter.Generate
//	chain := ComposeLLM(base, RetryMiddleware, MetricsMiddleware, LoggingMiddleware)
//	result, err := chain(ctx, call)
func ComposeLLM(base LLMFunc, middlewares ...LLMMiddleware) LLMFunc {
	chain := base
	for i := len(middlewares) - 1; i >= 0; i-- {
		m := middlewares[i]
		nextFn := chain
		chain = func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
			return m(ctx, call, nextFn)
		}
	}
	return chain
}

// --- Built-in Middleware ---

// RetryMiddleware wraps calls with exponential backoff retry.
func RetryMiddleware(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= MaxLLMRetries; attempt++ {
		resp, err := next(ctx, call)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Classify the error
		decision := classifyError(err)
		switch decision.Decision {
		case RetryDecisionFatal, RetryDecisionEmitToSession:
			return nil, err
		case RetryDecisionRetryCompaction:
			// Caller must handle compaction; return error for now
			return nil, err
		case RetryDecisionRetry, RetryDecisionRetryBackoff:
			if attempt < MaxLLMRetries {
				backoff := RetryBackoff(attempt + 1)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
			}
		}
	}
	return nil, fmt.Errorf("llm retry exhausted after %d attempts: %w", MaxLLMRetries, lastErr)
}

// MetricsMiddleware records LLM call metrics.
type MetricsRecorder interface {
	RecordLLMCall(model string, latency time.Duration, tokens int, err error)
}

// MetricsMiddlewareFunc creates a middleware that records metrics.
func MetricsMiddlewareFunc(recorder MetricsRecorder) LLMMiddleware {
	return func(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error) {
		start := time.Now()
		resp, err := next(ctx, call)
		latency := time.Since(start)
		tokens := 0
		if resp != nil {
			tokens = resp.TotalTokens
		}
		recorder.RecordLLMCall(call.Model, latency, tokens, err)
		return resp, err
	}
}

// LoggingMiddleware logs LLM calls and durations.
func LoggingMiddleware(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error) {
	start := time.Now()
	resp, err := next(ctx, call)
	latency := time.Since(start)
	if err != nil {
		log.Printf("[llm] model=%s msgs=%d latency=%v error=%v", call.Model, len(call.Messages), latency, err)
	} else {
		tokens := 0
		if resp != nil {
			tokens = resp.TotalTokens
		}
		log.Printf("[llm] model=%s msgs=%d latency=%v tokens=%d", call.Model, len(call.Messages), latency, tokens)
	}
	return resp, err
}

// ModelRouterMiddleware switches the model based on intent.
type ModelRouterMiddleware struct {
	routes map[string]string // intent → model
}

// NewModelRouter creates a model routing middleware.
func NewModelRouter(routes map[string]string) *ModelRouterMiddleware {
	return &ModelRouterMiddleware{routes: routes}
}

func (r *ModelRouterMiddleware) Handle(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error) {
	// Check if there's a route override in context
	if intent := ctx.Value(intentKey{}); intent != nil {
		if model, ok := r.routes[intent.(string)]; ok {
			call.Model = model
		}
	}
	return next(ctx, call)
}

type intentKey struct{}

// WithIntent returns a context carrying the intent for model routing.
func WithIntent(ctx context.Context, intent string) context.Context {
	return context.WithValue(ctx, intentKey{}, intent)
}

// --- Error Classification Helper ---

func classifyError(err error) RetryResult {
	if err == nil {
		return RetryResult{Decision: RetryDecisionFatal}
	}
	// Simplified classification; full implementation would check HTTP status codes
	errStr := err.Error()
	switch {
	case contains(errStr, "timeout") || contains(errStr, "deadline"):
		return RetryResult{Decision: RetryDecisionRetryBackoff, Reason: "timeout"}
	case contains(errStr, "rate limit") || contains(errStr, "429"):
		return RetryResult{Decision: RetryDecisionRetryBackoff, Reason: "rate limit"}
	case contains(errStr, "500") || contains(errStr, "502") || contains(errStr, "503"):
		return RetryResult{Decision: RetryDecisionRetryBackoff, Reason: "server error"}
	case contains(errStr, "context") && contains(errStr, "length"):
		return RetryResult{Decision: RetryDecisionRetryCompaction, Reason: "context overflow"}
	default:
		return RetryResult{Decision: RetryDecisionFatal, Reason: "unknown error"}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
