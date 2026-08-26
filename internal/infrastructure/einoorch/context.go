package einoorch

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/eval"
	"github.com/spray272598/code-agent/internal/domain/hook"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
)

type ctxKey int

const (
	ctxSessionID ctxKey = iota + 1
	ctxAutoApprove
	ctxEventSink
	ctxUserID
	ctxCrossCut
	ctxEvalCollector
)

// CrossCut holds optional domain services injected into GuardedTool (security + business).
type CrossCut struct {
	Hooks  *hook.Bus
	Audit  audit.Repository
	Cache  *domtool.ResultCache
	UserID string
}

// RunContext is permission + identity for one agent turn.
type RunContext struct {
	SessionID   string
	UserID      string
	AutoApprove bool
	Publish     EventSink
	Cross       *CrossCut
	// EvalCollector tracks per-session evaluation metrics.
	EvalCollector *eval.Collector
}

// WithRunContext packs session, approve, user, events, cross-cuts into ctx.
func WithRunContext(ctx context.Context, rc RunContext) context.Context {
	ctx = context.WithValue(ctx, ctxSessionID, rc.SessionID)
	ctx = context.WithValue(ctx, ctxAutoApprove, rc.AutoApprove)
	if rc.UserID != "" {
		ctx = context.WithValue(ctx, ctxUserID, rc.UserID)
	}
	if rc.Publish != nil {
		ctx = context.WithValue(ctx, ctxEventSink, rc.Publish)
	}
	if rc.Cross != nil {
		// merge UserID into cross if needed
		if rc.Cross.UserID == "" {
			rc.Cross.UserID = rc.UserID
		}
		ctx = context.WithValue(ctx, ctxCrossCut, rc.Cross)
	}
	if rc.EvalCollector != nil {
		ctx = context.WithValue(ctx, ctxEvalCollector, rc.EvalCollector)
	}
	return ctx
}

func sessionIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxSessionID).(string)
	return s
}

func autoApproveFrom(ctx context.Context) bool {
	b, _ := ctx.Value(ctxAutoApprove).(bool)
	return b
}

func userIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxUserID).(string)
	return s
}

func crossFrom(ctx context.Context) *CrossCut {
	c, _ := ctx.Value(ctxCrossCut).(*CrossCut)
	return c
}

func sinkFrom(ctx context.Context) EventSink {
	s, _ := ctx.Value(ctxEventSink).(EventSink)
	return s
}

func evalCollectorFrom(ctx context.Context) *eval.Collector {
	c, _ := ctx.Value(ctxEvalCollector).(*eval.Collector)
	return c
}

// WithSession puts permission context (compat helper).
func WithSession(ctx context.Context, sessionID string, autoApprove bool) context.Context {
	return WithRunContext(ctx, RunContext{SessionID: sessionID, AutoApprove: autoApprove})
}

// WithEventSink injects SSE publisher.
func WithEventSink(ctx context.Context, sink EventSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxEventSink, sink)
}
