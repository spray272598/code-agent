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

// sessionKey carries session / approve flags into tool InvokableRun.
type ctxKey int

const (
	ctxSessionID ctxKey = iota + 1
	ctxAutoApprove
)

// WithSession puts permission context for guarded tools.
func WithSession(ctx context.Context, sessionID string, autoApprove bool) context.Context {
	ctx = context.WithValue(ctx, ctxSessionID, sessionID)
	ctx = context.WithValue(ctx, ctxAutoApprove, autoApprove)
	return ctx
}

// GuardedTool adapts domain ITool → Eino InvokableTool, enforcing Guard on every call.
type GuardedTool struct {
	Inner domtool.ITool
	Guard *security.Guard
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
	args := map[string]any{}
	if argumentsInJSON != "" && argumentsInJSON != "null" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "invalid tool args json: " + err.Error(), nil
		}
	}
	// schema validate (domain)
	if err := domtool.ValidateArgs(t.Inner.InputSchema(), args); err != nil {
		return "validation error: " + err.Error(), nil
	}

	sessionID, _ := ctx.Value(ctxSessionID).(string)
	auto, _ := ctx.Value(ctxAutoApprove).(bool)
	name := t.Inner.Name()

	if t.Guard != nil && !auto {
		dec := t.Guard.Check(sessionID, name, args)
		switch dec.Action {
		case security.ActionDeny:
			return fmt.Sprintf("DENIED [%s]: %s", dec.Layer, dec.Reason), nil
		case security.ActionConfirm:
			p := t.Guard.CreatePending(sessionID, name, args, dec)
			return fmt.Sprintf("CONFIRM required [%s] tool=%s reason=%s id=%s — approve then continue",
				dec.Layer, name, dec.Reason, p.ID), nil
		}
	}

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
		out = append(out, &GuardedTool{Inner: t, Guard: guard})
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
		// fallback empty object
		return &jsonschema.Schema{Type: "object"}
	}
	if js.Type == "" {
		js.Type = "object"
	}
	return js
}
