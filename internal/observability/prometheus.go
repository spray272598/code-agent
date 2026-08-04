package observability

import (
	"fmt"
	"net/http"
	"strings"
)

// WritePrometheus exposes counters in Prometheus text exposition format (no extra deps).
func WritePrometheus(w http.ResponseWriter, r *http.Request) {
	_ = r
	m := Current().Snapshot()
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
	line("code_agent_chat_total", "Total chat requests", "counter", m["chat_total"])
	line("code_agent_chat_errors_total", "Chat errors", "counter", m["chat_errors"])
	line("code_agent_tool_calls_total", "Tool invocations", "counter", m["tool_calls"])
	line("code_agent_permission_deny_total", "Permission denials", "counter", m["permission_deny"])
	line("code_agent_memory_writes_total", "Memory writes", "counter", m["memory_writes"])
	line("code_agent_memory_reads_total", "Memory reads", "counter", m["memory_reads"])
	line("code_agent_tokens_total", "Tokens accounted", "counter", m["tokens_total"])
	line("code_agent_reflect_total", "Reflect invocations", "counter", m["reflect_total"])
	line("code_agent_compress_total", "Context compressions", "counter", m["compress_total"])
	line("code_agent_blob_offload_total", "Large tool results offloaded", "counter", m["blob_offload_total"])
	line("code_agent_llm_latency_avg_ms", "Average LLM latency ms", "gauge", m["llm_latency_avg_ms"])
	line("code_agent_tool_latency_avg_ms", "Average tool latency ms", "gauge", m["tool_latency_avg_ms"])
	line("code_agent_llm_calls_total", "LLM generate calls", "counter", m["llm_latency_count"])
	line("code_agent_tool_timed_total", "Timed tool calls", "counter", m["tool_latency_count"])
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if _, err := w.Write([]byte(b.String())); err != nil {
		LogError("prometheus write", err)
	}
}
