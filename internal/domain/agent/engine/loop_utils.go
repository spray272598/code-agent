package engine

// loop_utils.go – string helpers, ID generation, system prompt, and data
// conversion utilities used by the agent loop.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/types/common"
)

func defaultSystem() string {
	return `You are Code-Agent, a coding agent like Claude Code.
You work inside a sandboxed project workspace.

## ReAct protocol (required every turn)
You MUST reason actively before acting. Each assistant turn uses this format:

Thought: <your analysis of the goal, what you know, what to do next>
Action: {"name":"tool_name","args":{...}}
  or multiple tools: Action: [{"name":"...","args":{...}}, ...]
    (read-only tools execute in parallel; write/bash serially)
  pure JSON tool call(s) without the Action: label is also accepted
Final Answer: <user-facing answer when no more tools are needed>

After tools run, you will receive Observation(...): results. Then emit a new Thought and either another Action or Final Answer.
Do NOT skip Thought. Reflection on failure is part of Thought, not a separate mode.
Respect token budget: be concise; prefer Final Answer when enough evidence is collected.

## Tools
Core: read_file, write_file, edit_file, bash, glob, grep, memory_save, memory_search, delegate.
- memory_save / memory_search for durable user/project facts
- delegate for SubAgents (roles: explore|verify|general)
- edit_file supports multi-line exact replace and regex (regex=true)
- glob supports ** via doublestar; grep supports context (context_before/after or -C)

Prefer edit_file over full write for existing files. Be concise.
Dangerous operations require user confirmation. All tools (including MCP server__tool) go through permission checks.
If a tool fails: Thought should diagnose root cause and pick a different path/tool.

## Workspace switching
If a user asks to work on a project outside the current workspace, use the switch_workspace tool first:
Action: {"name":"switch_workspace","args":{"path":"D:/some/project"}}
After switching, all file tools (read_file, glob, grep, bash, etc.) will operate in the new workspace.
Do NOT attempt to access paths outside the workspace without switching first — they will be DENIED.`
}

// isContinue returns true when the user input is a continuation request.
func isContinue(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "继续" || s == "continue" || s == "ok" || s == "y" || s == "yes" || s == "继续执行"
}

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	return common.TruncateRunes(s, n)
}

// budget truncates a tool result to the max allowed size.
func budget(s string) string {
	return common.TruncateRunes(s, maxToolResultChars)
}

// chunkText splits s into chunks of size runes.
func chunkText(s string, size int) []string {
	r := []rune(s)
	if size <= 0 {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(r); i += size {
		j := i + size
		if j > len(r) {
			j = len(r)
		}
		out = append(out, string(r[i:j]))
	}
	return out
}

// mapsToChat converts message maps to ChatMessage slice.
func mapsToChat(history []map[string]any) []port.ChatMessage {
	out := make([]port.ChatMessage, 0, len(history))
	for _, m := range history {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if role == "" || content == "" || role == "system" {
			continue
		}
		cm := port.ChatMessage{Role: role, Content: content}
		if n, ok := m["toolName"].(string); ok {
			cm.Name = n
		}
		if id, ok := m["toolCallId"].(string); ok {
			cm.ToolCallID = id
		}
		out = append(out, cm)
	}
	return out
}

// estimateMaps sums estimated token counts across message maps.
func estimateMaps(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		if c, ok := m["content"].(string); ok {
			n += common.EstimateTokens(c)
		}
	}
	return n
}

// id returns a random ID with the given prefix.
func id(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

// now returns the current Unix timestamp in milliseconds.
func now() int64 { return time.Now().UnixMilli() }
