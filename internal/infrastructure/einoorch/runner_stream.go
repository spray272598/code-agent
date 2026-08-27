package einoorch

// runner_stream.go — streaming LLM response accumulation with partial tool-call argument merging.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
)

// runStream streams the agent response, accumulates text and tool-call deltas,
// and publishes intermediate events. Returns the final accumulated text.
func (r *Runner) runStream(ctx context.Context, ag *agentHandle, msgs []*schema.Message, publish EventSink, opts ...agent.AgentOption) (string, error) {
	sr, err := ag.Stream(ctx, msgs, opts...)
	if err != nil {
		return "", err
	}
	defer sr.Close()
	var b strings.Builder
	// Accumulate partial tool-call argument streams (index → name/args).
	type tcAcc struct {
		name string
		args strings.Builder
		id   string
	}
	byIdx := map[int]*tcAcc{}
	emittedName := map[string]bool{}

	for {
		msg, err := sr.Recv()
		if err != nil {
			// Preserve interrupt / real failures; only EOF is normal stream end.
			if errors.Is(err, io.EOF) {
				break
			}
			// Some providers close with generic error string; treat pure EOF-like ends only.
			if err.Error() == "EOF" {
				break
			}
			return strings.TrimSpace(b.String()), err
		}
		if msg == nil {
			continue
		}
		if msg.Content != "" {
			b.WriteString(msg.Content)
			publish(&engine.Event{Type: engine.EventTextDelta, Content: msg.Content, Timestamp: nowMs()})
		}
		// Streamed tool_call deltas (name may arrive before/without full args).
		for _, tc := range msg.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			acc, ok := byIdx[idx]
			if !ok {
				acc = &tcAcc{}
				byIdx[idx] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" && acc.name == "" {
				acc.name = tc.Function.Name
				if !emittedName[acc.name] {
					emittedName[acc.name] = true
					publish(&engine.Event{
						Type: engine.EventToolCall, SubType: acc.name,
						Content:   acc.name,
						Data:      map[string]any{"id": acc.id, "stream": true},
						Timestamp: nowMs(),
					})
				}
			}
			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	// Publish finalized tool-call args when stream ends with complete JSON fragments.
	for idx, acc := range byIdx {
		if acc == nil || acc.name == "" {
			continue
		}
		raw := acc.args.String()
		if raw == "" {
			continue
		}
		args := map[string]any{}
		_ = json.Unmarshal([]byte(raw), &args)
		publish(&engine.Event{
			Type: engine.EventToolCall, SubType: acc.name,
			Content:   "args_complete",
			Data:      map[string]any{"index": idx, "id": acc.id, "args": args, "args_raw": truncate(raw, ArgsRawMaxChars)},
			Timestamp: nowMs(),
		})
	}
	return strings.TrimSpace(b.String()), nil
}
