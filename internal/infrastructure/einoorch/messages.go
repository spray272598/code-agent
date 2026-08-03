package einoorch

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/types/common"
)

// mapsToSchema converts stored chat history maps to Eino messages.
// Preserves tool rows (toolName/toolCallId) so multi-turn tool context is not dropped.
func mapsToSchema(hist []map[string]any) []*schema.Message {
	out := make([]*schema.Message, 0, len(hist))
	for _, m := range hist {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		toolName, _ := m["toolName"].(string)
		if toolName == "" {
			toolName, _ = m["tool_name"].(string)
		}
		toolCallID, _ := m["toolCallId"].(string)
		if toolCallID == "" {
			toolCallID, _ = m["tool_call_id"].(string)
		}
		// skip empty non-tool
		if content == "" && role != "tool" && role != "assistant" {
			continue
		}
		switch role {
		case "user":
			out = append(out, schema.UserMessage(content))
		case "system":
			out = append(out, schema.SystemMessage(content))
		case "assistant":
			// optional embedded tool_calls in content as JSON array marker
			if tcs := extractToolCalls(m, content); len(tcs) > 0 {
				out = append(out, schema.AssistantMessage(contentWithoutToolJSON(content), tcs))
			} else {
				out = append(out, schema.AssistantMessage(content, nil))
			}
		case "tool":
			if toolCallID == "" {
				// synthesize stable-ish id so model can still see observation
				toolCallID = "hist_" + hash8(toolName+content)
			}
			opts := []schema.ToolMessageOption{}
			if toolName != "" {
				opts = append(opts, schema.WithToolName(toolName))
			}
			out = append(out, schema.ToolMessage(content, toolCallID, opts...))
		default:
			// unknown role → user-like
			if content != "" {
				out = append(out, schema.UserMessage(content))
			}
		}
	}
	return out
}

// schemaToEstimateTokens rough token count for budget.
func schemaToEstimateTokens(msgs []*schema.Message) int {
	n := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		n += common.EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			n += common.EstimateTokens(tc.Function.Name) + common.EstimateTokens(tc.Function.Arguments)
		}
	}
	return n
}

// trimSchemaMessages keeps system + last keepTail messages within token budget.
func trimSchemaMessages(msgs []*schema.Message, tokenBudget, keepTail int) []*schema.Message {
	if tokenBudget <= 0 {
		tokenBudget = 16000
	}
	if keepTail <= 0 {
		keepTail = 12
	}
	if schemaToEstimateTokens(msgs) <= tokenBudget && len(msgs) <= keepTail+4 {
		return msgs
	}
	var system []*schema.Message
	var rest []*schema.Message
	for _, m := range msgs {
		if m != nil && m.Role == schema.System {
			system = append(system, m)
		} else {
			rest = append(rest, m)
		}
	}
	if len(rest) > keepTail {
		rest = rest[len(rest)-keepTail:]
	}
	// further drop from front until budget
	for schemaToEstimateTokens(append(system, rest...)) > tokenBudget && len(rest) > 2 {
		rest = rest[1:]
	}
	// ensure we don't start with orphan tool messages
	for len(rest) > 0 && rest[0] != nil && rest[0].Role == schema.Tool {
		rest = rest[1:]
	}
	out := make([]*schema.Message, 0, len(system)+len(rest)+1)
	out = append(out, system...)
	if len(rest) < len(msgs)-len(system) {
		out = append(out, schema.UserMessage(
			fmt.Sprintf("[CONTEXT_TRIMMED] Earlier turns dropped to fit token budget (~%d). Continue from recent context.", tokenBudget),
		))
	}
	out = append(out, rest...)
	return out
}

func extractToolCalls(m map[string]any, content string) []schema.ToolCall {
	// from map field
	if raw, ok := m["toolCalls"]; ok {
		return parseToolCallsAny(raw)
	}
	if raw, ok := m["tool_calls"]; ok {
		return parseToolCallsAny(raw)
	}
	// from content prefix TOOL_CALLS_JSON:
	const mark = "TOOL_CALLS_JSON:"
	if i := strings.Index(content, mark); i >= 0 {
		js := strings.TrimSpace(content[i+len(mark):])
		if j := strings.Index(js, "\n"); j >= 0 {
			js = js[:j]
		}
		var tcs []schema.ToolCall
		if json.Unmarshal([]byte(js), &tcs) == nil {
			return tcs
		}
	}
	return nil
}

func parseToolCallsAny(raw any) []schema.ToolCall {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var tcs []schema.ToolCall
	if json.Unmarshal(b, &tcs) == nil && len(tcs) > 0 {
		return tcs
	}
	return nil
}

func contentWithoutToolJSON(content string) string {
	const mark = "TOOL_CALLS_JSON:"
	if i := strings.Index(content, mark); i >= 0 {
		return strings.TrimSpace(content[:i])
	}
	return content
}

func hash8(s string) string {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}
