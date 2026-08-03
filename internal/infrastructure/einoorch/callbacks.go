package einoorch

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	ub "github.com/cloudwego/eino/utils/callbacks"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

// EventSink publishes engine events (SSE).
type EventSink func(*engine.Event)

// runStats accumulates tool metrics during one Eino run.
type runStats struct {
	toolCalls atomic.Int32
	toolErrs  atomic.Int32
}

// agentOptions builds compose callbacks that map Eino model/tool lifecycle → SSE events.
func agentOptions(publish EventSink, stats *runStats) agent.AgentOption {
	if publish == nil {
		publish = func(*engine.Event) {}
	}
	if stats == nil {
		stats = &runStats{}
	}
	now := func() int64 { return time.Now().UnixMilli() }

	modelH := &ub.ModelCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *model.CallbackInput) context.Context {
			n := 0
			if input != nil {
				n = len(input.Messages)
			}
			publish(&engine.Event{
				Type: engine.EventThought, Content: "eino ChatModel start",
				Data: map[string]any{"messages": n, "node": nameOf(info)}, Timestamp: now(),
			})
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *model.CallbackOutput) context.Context {
			if output == nil || output.Message == nil {
				return ctx
			}
			msg := output.Message
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					args := map[string]any{}
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
					publish(&engine.Event{
						Type: engine.EventAction, SubType: tc.Function.Name,
						Content: tc.Function.Name, Data: args, Timestamp: now(),
					})
					publish(&engine.Event{
						Type: engine.EventToolCall, SubType: tc.Function.Name,
						Content: tc.Function.Name, Data: args, Timestamp: now(),
					})
				}
			} else if c := truncate(msg.Content, 200); c != "" {
				publish(&engine.Event{
					Type: engine.EventThought, Content: "model: " + c, Timestamp: now(),
				})
			}
			return ctx
		},
		OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[*model.CallbackOutput]) context.Context {
			// consume stream async for text deltas (best-effort)
			go func() {
				defer output.Close()
				for {
					chunk, err := output.Recv()
					if err != nil {
						return
					}
					if chunk == nil || chunk.Message == nil {
						continue
					}
					if t := chunk.Message.Content; t != "" {
						publish(&engine.Event{Type: engine.EventTextDelta, Content: t, Timestamp: now()})
					}
					for _, tc := range chunk.Message.ToolCalls {
						if tc.Function.Name != "" {
							publish(&engine.Event{
								Type: engine.EventToolCall, SubType: tc.Function.Name,
								Content: tc.Function.Name, Timestamp: now(),
							})
						}
					}
				}
			}()
			return ctx
		},
	}

	toolH := &ub.ToolCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *einotool.CallbackInput) context.Context {
			name := nameOf(info)
			argsPreview := ""
			if input != nil {
				argsPreview = truncate(input.ArgumentsInJSON, 300)
			}
			publish(&engine.Event{
				Type: engine.EventToolCall, SubType: name, Content: "exec " + name,
				Data: map[string]any{"args": argsPreview}, Timestamp: now(),
			})
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *einotool.CallbackOutput) context.Context {
			stats.toolCalls.Add(1)
			name := nameOf(info)
			resp := ""
			if output != nil {
				resp = output.Response
			}
			if looksError(resp) {
				stats.toolErrs.Add(1)
			}
			// permission events
			if stringsHasPrefix(resp, "CONFIRM") {
				publish(&engine.Event{
					Type: engine.EventPermission, SubType: "confirm", Content: truncate(resp, 500),
					Timestamp: now(), Completed: true,
				})
			} else if stringsHasPrefix(resp, "DENIED") {
				publish(&engine.Event{
					Type: engine.EventPermission, SubType: "deny", Content: truncate(resp, 400),
					Timestamp: now(),
				})
			}
			publish(&engine.Event{
				Type: engine.EventObservation, SubType: name, Content: truncate(resp, 800), Timestamp: now(),
			})
			publish(&engine.Event{
				Type: engine.EventToolResult, SubType: name, Content: truncate(resp, 800), Timestamp: now(),
			})
			return ctx
		},
	}

	cb := react.BuildAgentCallback(modelH, toolH)
	return agent.WithComposeOptions(compose.WithCallbacks(cb))
}

func nameOf(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	if info.Name != "" {
		return info.Name
	}
	return string(info.Component)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func looksError(s string) bool {
	return strings.HasPrefix(s, "DENIED") || strings.HasPrefix(s, "validation") ||
		strings.HasPrefix(s, "error") || strings.HasPrefix(s, "Error") ||
		strings.Contains(s, "failed")
}

func stringsHasPrefix(s, p string) bool {
	return strings.HasPrefix(s, p)
}
