package einoorch

import (
	"context"
	"encoding/json"
	"fmt"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"github.com/spray272598/code-agent/internal/domain/security"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
)

type ctxKey int

const (
	ctxSessionID ctxKey = iota + 1
	ctxAutoApprove
	ctxEventSink
)

// ConfirmInfo is persisted across Eino tool interrupt for HITL resume.
type ConfirmInfo struct {
	PendingID string         `json:"pendingId"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Reason    string         `json:"reason"`
	SessionID string         `json:"sessionId"`
}

// WithSession puts permission context for guarded tools.
func WithSession(ctx context.Context, sessionID string, autoApprove bool) context.Context {
	ctx = context.WithValue(ctx, ctxSessionID, sessionID)
	ctx = context.WithValue(ctx, ctxAutoApprove, autoApprove)
	return ctx
}

// WithEventSink injects SSE publisher into tool executions.
func WithEventSink(ctx context.Context, sink EventSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxEventSink, sink)
}

// GuardedTool adapts domain ITool → Eino InvokableTool, enforcing Guard on every call.
// On CONFIRM: uses tool.Interrupt when possible so ReAct graph can stop for HITL;
// always CreatePending so existing /permission/approve + 继续 path works.
type GuardedTool struct {
	Inner       domtool.ITool
	Guard       *security.Guard
	UseInterrupt bool // try Eino Interrupt on confirm (default true)
}

var _ einotool.InvokableTool = (*GuardedTool)(nil)

func (t *GuardedTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	if t.Inner == nil {
		return nil, fmt.Errorf("nil tool")
	}
	info := &schema.ToolInfo{
		Name: t.Inner.Name(),
		Desc: t.Inner.Description(),
	}
	if s := t.Inner.InputSchema(); s != nil {
		if js := mapToJSONSchema(s); js != nil {
			info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(js)
		}
	}
	return info, nil
}

func (t *GuardedTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	if t.Inner == nil {
		return "", fmt.Errorf("nil tool")
	}
	name := t.Inner.Name()

	// Resume path: after interrupt, Eino re-invokes tool with saved state
	if was, has, st := einotool.GetInterruptState[ConfirmInfo](ctx); was && has {
		// user approved via Guard session allow → execute
		sessionID := st.SessionID
		args := st.Args
		if args == nil {
			args = map[string]any{}
		}
		// if still not auto-approved in session, re-confirm
		auto, _ := ctx.Value(ctxAutoApprove).(bool)
		if t.Guard != nil && !auto {
			dec := t.Guard.Check(sessionID, name, args)
			if dec.Action != security.ActionAllow {
				// still blocked
				msg := fmt.Sprintf("CONFIRM still required tool=%s id=%s", name, st.PendingID)
				return msg, nil
			}
		}
		return t.exec(ctx, args)
	}

	args := map[string]any{}
	if argumentsInJSON != "" && argumentsInJSON != "null" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "invalid tool args json: " + err.Error(), nil
		}
	}
	if err := domtool.ValidateArgs(t.Inner.InputSchema(), args); err != nil {
		return "validation error: " + err.Error(), nil
	}

	sessionID, _ := ctx.Value(ctxSessionID).(string)
	auto, _ := ctx.Value(ctxAutoApprove).(bool)

	if t.Guard != nil && !auto {
		dec := t.Guard.Check(sessionID, name, args)
		switch dec.Action {
		case security.ActionDeny:
			return fmt.Sprintf("DENIED [%s]: %s", dec.Layer, dec.Reason), nil
		case security.ActionConfirm:
			p := t.Guard.CreatePending(sessionID, name, args, dec)
			info := ConfirmInfo{
				PendingID: p.ID, Tool: name, Args: args,
				Reason: dec.Reason, SessionID: sessionID,
			}
			// Prefer Eino StatefulInterrupt so ReAct graph can stop for HITL.
			// If interrupt machinery isn't armed (no checkpoint), fall back to string result.
			if t.UseInterrupt {
				if err := einotool.StatefulInterrupt(ctx, info, info); err != nil {
					// Interrupt returns a signal error that MUST be propagated
					return "", err
				}
			}
			return fmt.Sprintf("CONFIRM required [%s] tool=%s reason=%s id=%s — approve then continue",
				dec.Layer, name, dec.Reason, p.ID), nil
		}
	}

	return t.exec(ctx, args)
}

func (t *GuardedTool) exec(ctx context.Context, args map[string]any) (string, error) {
	res, err := t.Inner.Execute(ctx, args)
	if err != nil {
		return err.Error(), nil
	}
	if res.IsError {
		return res.Text, nil
	}
	return res.Text, nil
}

// WrapRegistry converts all domain tools to Eino BaseTool list.
func WrapRegistry(reg *domtool.MapRegistry, guard *security.Guard) []einotool.BaseTool {
	if reg == nil {
		return nil
	}
	list := reg.List()
	out := make([]einotool.BaseTool, 0, len(list))
	for _, t := range list {
		if t == nil {
			continue
		}
		out = append(out, &GuardedTool{Inner: t, Guard: guard, UseInterrupt: true})
	}
	return out
}

func mapToJSONSchema(m map[string]any) *jsonschema.Schema {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	js := &jsonschema.Schema{}
	if err := json.Unmarshal(b, js); err != nil {
		return &jsonschema.Schema{Type: "object"}
	}
	if js.Type == "" {
		js.Type = "object"
	}
	return js
}
