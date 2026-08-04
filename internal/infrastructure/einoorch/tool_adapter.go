package einoorch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/security"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/observability"
)

// ConfirmInfo is persisted across Eino tool interrupt for HITL resume.
type ConfirmInfo struct {
	PendingID string         `json:"pendingId"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Reason    string         `json:"reason"`
	SessionID string         `json:"sessionId"`
}

// GuardedTool adapts domain ITool → Eino InvokableTool with full cross-cutting:
// validate → permission → PreToolUse abort → cache → execute → PostToolUse → audit → metrics.
type GuardedTool struct {
	Inner        domtool.ITool
	Guard        *security.Guard
	UseInterrupt bool
	// optional shared cross-cut (also overridable via context CrossCut)
	Cross *CrossCut
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
	cross := t.resolveCross(ctx)
	sessionID := sessionIDFrom(ctx)
	auto := autoApproveFrom(ctx)
	userID := userIDFrom(ctx)
	if userID == "" && cross != nil {
		userID = cross.UserID
	}

	// --- Resume after interrupt ---
	if was, has, st := einotool.GetInterruptState[ConfirmInfo](ctx); was && has {
		sessionID = st.SessionID
		args := st.Args
		if args == nil {
			args = map[string]any{}
		}
		if t.Guard != nil && !auto {
			dec := t.Guard.Check(sessionID, name, args)
			if dec.Action != security.ActionAllow {
				return fmt.Sprintf("CONFIRM still required tool=%s id=%s", name, st.PendingID), nil
			}
		}
		return t.execCross(ctx, name, args, sessionID, userID, cross)
	}

	// --- Parse + validate ---
	args := map[string]any{}
	if argumentsInJSON != "" && argumentsInJSON != "null" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "validation error: invalid tool args json: " + err.Error(), nil
		}
	}
	if err := domtool.ValidateArgs(t.Inner.InputSchema(), args); err != nil {
		observability.Current().AddToolCalls(1)
		t.audit(ctx, cross, userID, sessionID, name, err.Error(), "validation", 0)
		return "validation error: " + err.Error(), nil
	}

	// --- Permission ---
	if t.Guard != nil && !auto {
		dec := t.Guard.Check(sessionID, name, args)
		if cross != nil && cross.Hooks != nil {
			cross.Hooks.Emit(ctx, hook.Event{
				Point: hook.Permission, SessionID: sessionID, Tool: name, Args: args, Decision: string(dec.Action),
			})
		}
		switch dec.Action {
		case security.ActionDeny:
			observability.Current().AddPermissionDeny(1)
			t.audit(ctx, cross, userID, sessionID, name, dec.Reason, "deny", 0)
			return fmt.Sprintf("DENIED [%s]: %s", dec.Layer, dec.Reason), nil
		case security.ActionConfirm:
			p := t.Guard.CreatePending(sessionID, name, args, dec)
			info := ConfirmInfo{
				PendingID: p.ID, Tool: name, Args: args,
				Reason: dec.Reason, SessionID: sessionID,
			}
			t.audit(ctx, cross, userID, sessionID, name, dec.Reason, "confirm", 0)
			if t.UseInterrupt {
				if err := einotool.StatefulInterrupt(ctx, info, info); err != nil {
					return "", err
				}
			}
			return fmt.Sprintf("CONFIRM required [%s] tool=%s reason=%s id=%s — approve then continue",
				dec.Layer, name, dec.Reason, p.ID), nil
		}
	}

	// --- PreToolUse hook (can abort) ---
	if cross != nil && cross.Hooks != nil {
		if aborted, err := cross.Hooks.EmitCheck(ctx, hook.Event{
			Point: hook.PreToolUse, SessionID: sessionID, Tool: name, Args: args,
		}); aborted {
			msg := "HOOK_ABORT: " + err.Error()
			t.audit(ctx, cross, userID, sessionID, name, msg, "abort", 0)
			return msg, nil
		}
	}

	// --- Cache (read-only) ---
	if cross != nil && cross.Cache != nil {
		if hit, ok := cross.Cache.Get(name, args); ok {
			observability.Current().AddToolCalls(1)
			t.audit(ctx, cross, userID, sessionID, name, truncate(hit, 120), "cache", 0)
			return hit, nil
		}
	}

	return t.execCross(ctx, name, args, sessionID, userID, cross)
}

func (t *GuardedTool) execCross(ctx context.Context, name string, args map[string]any, sessionID, userID string, cross *CrossCut) (string, error) {
	t0 := time.Now()
	observability.Current().AddToolCalls(1)

	res, err := t.Inner.Execute(ctx, args)
	lat := time.Since(t0)
	observability.Current().ObserveTool(lat)

	text := ""
	decision := "ok"
	if err != nil {
		text = err.Error()
		decision = "error"
	} else if res.IsError {
		text = res.Text
		decision = "error"
	} else {
		text = res.Text
	}

	// cache successful read-only
	if decision == "ok" && cross != nil && cross.Cache != nil {
		cross.Cache.Put(name, args, text)
	}

	// PostToolUse
	if cross != nil && cross.Hooks != nil {
		cross.Hooks.Emit(ctx, hook.Event{
			Point: hook.PostToolUse, SessionID: sessionID, Tool: name, Args: args, Result: text,
		})
	}
	t.audit(ctx, cross, userID, sessionID, name, truncate(text, 300), decision, lat.Milliseconds())
	return text, nil
}

func (t *GuardedTool) resolveCross(ctx context.Context) *CrossCut {
	if c := crossFrom(ctx); c != nil {
		// merge struct defaults
		if t.Cross != nil {
			if c.Hooks == nil {
				c.Hooks = t.Cross.Hooks
			}
			if c.Audit == nil {
				c.Audit = t.Cross.Audit
			}
			if c.Cache == nil {
				c.Cache = t.Cross.Cache
			}
		}
		return c
	}
	return t.Cross
}

func (t *GuardedTool) audit(ctx context.Context, cross *CrossCut, userID, sessionID, toolName, detail, decision string, latencyMs int64) {
	if cross == nil || cross.Audit == nil {
		return
	}
	if err := cross.Audit.Append(ctx, audit.Entry{
		UserID: userID, SessionID: sessionID, Action: "tool_call",
		Tool: toolName, Detail: detail, Decision: decision, LatencyMs: latencyMs,
	}); err != nil {
		observability.LogError("audit append tool_call", err)
	}
}

// WrapRegistry converts domain tools with shared Guard + CrossCut.
func WrapRegistry(reg *domtool.MapRegistry, guard *security.Guard) []einotool.BaseTool {
	return WrapRegistryCross(reg, guard, nil)
}

// WrapRegistryCross attaches hooks/audit/cache to every tool.
func WrapRegistryCross(reg *domtool.MapRegistry, guard *security.Guard, cross *CrossCut) []einotool.BaseTool {
	if reg == nil {
		return nil
	}
	list := reg.List()
	out := make([]einotool.BaseTool, 0, len(list))
	for _, t := range list {
		if t == nil {
			continue
		}
		out = append(out, &GuardedTool{
			Inner: t, Guard: guard, UseInterrupt: true, Cross: cross,
		})
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
