package engine

// loop_tools.go – tool-call parsing, formatting, fingerprinting, and JSON
// helpers used by the agent loop.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// formatTools renders a list of tool descriptions for the system prompt.
func formatTools(list []map[string]string) string {
	var b strings.Builder
	for _, t := range list {
		b.WriteString("- ")
		b.WriteString(t["name"])
		b.WriteString(": ")
		b.WriteString(t["description"])
		b.WriteString("\n")
	}
	return b.String()
}

// parseToolCalls extracts tool calls from an LLM response string.
func parseToolCalls(response string) []port.ToolCall {
	response = strings.TrimSpace(response)
	if response == "" {
		return nil
	}
	if i := strings.Index(response, "```"); i >= 0 {
		rest := response[i+3:]
		if strings.HasPrefix(strings.ToLower(rest), "json") {
			rest = rest[4:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			response = strings.TrimSpace(rest[:j])
		}
	}
	var single struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(response), &single); err == nil && single.Name != "" {
		if single.Args == nil {
			single.Args = map[string]any{}
		}
		return []port.ToolCall{{Name: single.Name, Args: single.Args}}
	}
	var multi []struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(response), &multi); err == nil {
		var calls []port.ToolCall
		for _, m := range multi {
			if m.Name != "" {
				if m.Args == nil {
					m.Args = map[string]any{}
				}
				calls = append(calls, port.ToolCall{Name: m.Name, Args: m.Args})
			}
		}
		if len(calls) > 0 {
			return calls
		}
	}
	if i := strings.Index(response, "{"); i >= 0 {
		if j := strings.LastIndex(response, "}"); j > i {
			return parseToolCalls(response[i : j+1])
		}
	}
	return nil
}

// toolsFingerprint returns a stable fingerprint of registered tool names.
func toolsFingerprint(reg *tool.MapRegistry) string {
	if reg == nil {
		return ""
	}
	list := reg.Descriptions()
	var b strings.Builder
	for _, t := range list {
		b.WriteString(t["name"])
		b.WriteByte(',')
	}
	return b.String()
}

// ensureID guarantees a tool call has an ID field set.
func ensureID(tc port.ToolCall) string {
	if tc.ID != "" {
		return tc.ID
	}
	h := sha256.Sum256([]byte(tc.Name + mustJSON(tc.Args)))
	return "call_" + hex.EncodeToString(h[:6])
}

// mustJSON marshals v to JSON, swallowing errors.
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// isToolFail returns true when the tool result text indicates failure.
func isToolFail(s string) bool {
	ls := strings.ToLower(s)
	return strings.Contains(ls, "error") || strings.Contains(ls, "failed") ||
		strings.Contains(s, "失败") || strings.Contains(s, "不存在") ||
		strings.Contains(s, "not found") || strings.Contains(s, "DENIED") ||
		strings.HasPrefix(s, "tool not found")
}

// toolSig returns a SHA-256 fingerprint of a batch of tool calls.
func toolSig(calls []port.ToolCall) string {
	h := sha256.New()
	for _, c := range calls {
		h.Write([]byte(c.Name))
		b, _ := json.Marshal(c.Args)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
