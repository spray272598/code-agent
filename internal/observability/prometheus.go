package observability

import (
	"fmt"
	"net/http"
	"strings"
)

// WritePrometheus exposes counters in Prometheus text exposition format (no extra deps).
func WritePrometheus(w http.ResponseWriter, r *http.Request) {
	_ = r
	m := Global
	llmN := m.LLMLatencyCount.Load()
	toolN := m.ToolLatencyCount.Load()
	var llmAvg, toolAvg int64
	if llmN > 0 {
		llmAvg = m.LLMLatencySumMs.Load() / llmN
	}
	if toolN > 0 {
		toolAvg = m.ToolLatencySumMs.Load() / toolN
	}
	var b strings.Builder
	line := func(name, help, typ string, val any) {
		b.WriteString("# HELP ")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(help)
		b.WriteString("\n# TYPE ")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(typ)
		b.WriteString("\n")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(fmt.Sprint(val))
		b.WriteString("\n")
	}
	line("code_agent_chat_total", "Total chat requests", "counter", m.ChatTotal.Load())
	line("code_agent_chat_errors_total", "Chat errors", "counter", m.ChatErrors.Load())
	line("code_agent_tool_calls_total", "Tool invocations", "counter", m.ToolCalls.Load())
	line("code_agent_permission_deny_total", "Permission denials", "counter", m.PermissionDeny.Load())
	line("code_agent_memory_writes_total", "Memory writes", "counter", m.MemoryWrites.Load())
	line("code_agent_memory_reads_total", "Memory reads", "counter", m.MemoryReads.Load())
	line("code_agent_tokens_total", "Tokens accounted", "counter", m.TokensTotal.Load())
	line("code_agent_reflect_total", "Reflect invocations", "counter", m.ReflectTotal.Load())
	line("code_agent_compress_total", "Context compressions", "counter", m.CompressTotal.Load())
	line("code_agent_blob_offload_total", "Large tool results offloaded", "counter", m.BlobOffload.Load())
	line("code_agent_llm_latency_avg_ms", "Average LLM latency ms", "gauge", llmAvg)
	line("code_agent_tool_latency_avg_ms", "Average tool latency ms", "gauge", toolAvg)
	line("code_agent_llm_calls_total", "LLM generate calls", "counter", llmN)
	line("code_agent_tool_timed_total", "Timed tool calls", "counter", toolN)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
