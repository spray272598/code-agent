package llm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

func TestComposeLLMNoMiddleware(t *testing.T) {
	called := false
	base := func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
		called = true
		return &LLMResponse{Content: "ok"}, nil
	}

	chain := ComposeLLM(base)
	resp, err := chain(context.Background(), LLMCall{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("base not called")
	}
	if resp.Content != "ok" {
		t.Errorf("expected ok, got %s", resp.Content)
	}
}

func TestComposeLLMWithMiddleware(t *testing.T) {
	var order []string

	base := func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
		order = append(order, "base")
		return &LLMResponse{Content: "ok"}, nil
	}
	m1 := func(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error) {
		order = append(order, "m1-pre")
		resp, err := next(ctx, call)
		order = append(order, "m1-post")
		return resp, err
	}
	m2 := func(ctx context.Context, call LLMCall, next LLMFunc) (*LLMResponse, error) {
		order = append(order, "m2-pre")
		resp, err := next(ctx, call)
		order = append(order, "m2-post")
		return resp, err
	}

	chain := ComposeLLM(base, m1, m2)
	_, err := chain(context.Background(), LLMCall{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"m1-pre", "m2-pre", "base", "m2-post", "m1-post"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d, got %d: %v", len(expected), len(order), order)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Errorf("index %d: expected %s, got %s", i, expected[i], order[i])
		}
	}
}

func TestLoggingMiddleware(t *testing.T) {
	base := func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
		return &LLMResponse{Content: "ok", TotalTokens: 100}, nil
	}

	// Should not panic
	chain := ComposeLLM(base, LoggingMiddleware)
	_, err := chain(context.Background(), LLMCall{Model: "test", Messages: []port.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMetricsMiddlewareFunc(t *testing.T) {
	var recorded struct {
		model   string
		latency time.Duration
		tokens  int
		err     error
	}
	recorder := &testMetricsRecorder{fn: func(model string, latency time.Duration, tokens int, err error) {
		recorded.model = model
		recorded.latency = latency
		recorded.tokens = tokens
		recorded.err = err
	}}

	base := func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
		return &LLMResponse{Content: "ok", TotalTokens: 50}, nil
	}

	chain := ComposeLLM(base, MetricsMiddlewareFunc(recorder))
	_, err := chain(context.Background(), LLMCall{Model: "gpt-4"})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.model != "gpt-4" {
		t.Errorf("expected gpt-4, got %s", recorded.model)
	}
	if recorded.tokens != 50 {
		t.Errorf("expected 50 tokens, got %d", recorded.tokens)
	}
}

func TestRetryMiddlewareSuccess(t *testing.T) {
	var attempts atomic.Int32
	base := func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
		n := attempts.Add(1)
		if n < 3 {
			return nil, errors.New("timeout")
		}
		return &LLMResponse{Content: "ok"}, nil
	}

	chain := ComposeLLM(base, RetryMiddleware)
	resp, err := chain(context.Background(), LLMCall{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected ok, got %s", resp.Content)
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestModelRouterMiddleware(t *testing.T) {
	router := NewModelRouter(map[string]string{
		"code": "gpt-4",
		"chat": "gpt-3.5-turbo",
	})

	var capturedModel string
	base := func(ctx context.Context, call LLMCall) (*LLMResponse, error) {
		capturedModel = call.Model
		return &LLMResponse{}, nil
	}

	ctx := WithIntent(context.Background(), "code")
	chain := ComposeLLM(base, router.Handle)
	_, err := chain(ctx, LLMCall{Model: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if capturedModel != "gpt-4" {
		t.Errorf("expected gpt-4, got %s", capturedModel)
	}
}

type testMetricsRecorder struct {
	fn func(model string, latency time.Duration, tokens int, err error)
}

func (r *testMetricsRecorder) RecordLLMCall(model string, latency time.Duration, tokens int, err error) {
	r.fn(model, latency, tokens, err)
}
