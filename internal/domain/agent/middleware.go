package agent

import (
	"context"
	"fmt"
	"time"
)

// Waterfall is a chain-of-responsibility middleware pattern inspired by
// DeepSeek Harness. Each handler calls next() to continue the chain;
// returning early stops execution.
//
// Usage:
//
//	wf := NewWaterfall()
//	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
//	    // pre-processing
//	    return w.Next(ctx)
//	})
//	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
//	    // actual work
//	    return nil
//	})
//	err := wf.Run(ctx, initialData)
type WaterfallContext struct {
	// Data carries the mutable payload through the chain.
	Data any
	// Index is the current handler index.
	Index int
	// handlers is the list of middleware functions.
	handlers []WaterfallHandler
	// abort stops the chain.
	abort bool
}

// WaterfallHandler is a single middleware function.
type WaterfallHandler func(ctx context.Context, w *WaterfallContext) error

// Waterfall chains middleware handlers.
type Waterfall struct {
	handlers []WaterfallHandler
}

// NewWaterfall creates a new empty waterfall.
func NewWaterfall() *Waterfall {
	return &Waterfall{}
}

// Use adds a middleware handler to the chain.
func (wf *Waterfall) Use(h WaterfallHandler) {
	wf.handlers = append(wf.handlers, h)
}

// Run executes the waterfall chain with the given initial data.
func (wf *Waterfall) Run(ctx context.Context, data any) error {
	w := &WaterfallContext{
		Index:    -1,
		Data:     data,
		handlers: wf.handlers,
	}
	return w.Next(ctx)
}

// Next advances to the next handler in the chain.
func (w *WaterfallContext) Next(ctx context.Context) error {
	w.Index++
	if w.Index >= len(w.handlers) {
		return nil
	}
	if w.abort {
		return nil
	}
	return w.handlers[w.Index](ctx, w)
}

// Abort stops the waterfall chain. Subsequent Next() calls are no-ops.
func (w *WaterfallContext) Abort() {
	w.abort = true
}

// IsAborted returns true if Abort() was called.
func (w *WaterfallContext) IsAborted() bool {
	return w.abort
}

// --- Typed helpers for common waterfall patterns ---

// ToolExecData is the payload for tool execution waterfalls.
type ToolExecData struct {
	ToolName string
	Args     map[string]any
	Result   string
	Error    error
	Deny     bool
	DenyMsg  string
}

// GuardWaterfall is a pre-configured waterfall for Guard Check interception.
// It wraps the existing Guard.Check with an extensible middleware chain.
type GuardWaterfall struct {
	wf *Waterfall
}

// NewGuardWaterfall creates a guard waterfall with the given interceptors.
func NewGuardWaterfall(interceptors ...WaterfallHandler) *GuardWaterfall {
	wf := NewWaterfall()
	for _, i := range interceptors {
		wf.Use(i)
	}
	return &GuardWaterfall{wf: wf}
}

// Check runs the waterfall and returns whether the tool call should proceed.
func (gw *GuardWaterfall) Check(ctx context.Context, data *ToolExecData) bool {
	gw.wf.Run(ctx, data)
	return !data.Deny
}

// LLMRequestData is the payload for LLM request waterfalls.
type LLMRequestData struct {
	SystemPrompt string
	Messages     []any
	Model        string
	Temperature  float64
	MaxTokens    int
	// Modified can be set by middleware to override fields.
	Modified bool
}

// --- Pre-built middleware ---

// TimeoutMiddleware adds a deadline to the context if not already set.
func TimeoutMiddleware(d time.Duration) WaterfallHandler {
	return func(ctx context.Context, w *WaterfallContext) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return w.Next(ctx)
	}
}

// LoggingMiddleware logs handler execution via the context.
func LoggingMiddleware(name string) WaterfallHandler {
	return func(ctx context.Context, w *WaterfallContext) error {
		start := time.Now()
		err := w.Next(ctx)
		_ = fmt.Sprintf("[waterfall:%s] index=%d duration=%v err=%v", name, w.Index, time.Since(start), err)
		return err
	}
}
