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
	line("code_agent_quota_deny_total", "Token-quota denials (user daily limit)", "counter", m["quota_deny_total"])
	line("code_agent_mcp_cache_hits_total", "MCP tool cache hits", "counter", m["mcp_cache_hits"])
	line("code_agent_mcp_cache_misses_total", "MCP tool cache misses", "counter", m["mcp_cache_misses"])
	line("code_agent_mcp_tool_success_total", "MCP tool successful calls", "counter", m["mcp_tool_success"])
	line("code_agent_mcp_tool_errors_total", "MCP tool failed calls", "counter", m["mcp_tool_errors"])
	line("code_agent_circuit_breaker_transitions_total", "Circuit breaker state transitions", "counter", m["circuit_breaker_transitions"])
	line("code_agent_llm_latency_avg_ms", "Average LLM latency ms", "gauge", m["llm_latency_avg_ms"])
	line("code_agent_tool_latency_avg_ms", "Average tool latency ms", "gauge", m["tool_latency_avg_ms"])
	line("code_agent_llm_calls_total", "LLM generate calls", "counter", m["llm_latency_count"])
	line("code_agent_tool_timed_total", "Timed tool calls", "counter", m["tool_latency_count"])

	// SLO gauges.
	sloStates := GlobalSLO.AllStates()
	if len(sloStates) > 0 {
		b.WriteString("# HELP code_agent_slo_latency_p99_ms SLO P99 latency per service\n")
		b.WriteString("# TYPE code_agent_slo_latency_p99_ms gauge\n")
		for _, s := range sloStates {
			b.WriteString(fmt.Sprintf("code_agent_slo_latency_p99_ms{slo=\"%s\",status=\"%s\"} %d\n",
				s.Name, s.Status, s.LatencyP99Ms))
		}
		b.WriteString("# HELP code_agent_slo_error_rate SLO error rate per service (0-1)\n")
		b.WriteString("# TYPE code_agent_slo_error_rate gauge\n")
		for _, s := range sloStates {
			b.WriteString(fmt.Sprintf("code_agent_slo_error_rate{slo=\"%s\",status=\"%s\"} %.4f\n",
				s.Name, s.Status, s.ErrorRate))
		}
		b.WriteString("# HELP code_agent_slo_budget_burn SLO error budget burn rate (0-100)\n")
		b.WriteString("# TYPE code_agent_slo_budget_burn gauge\n")
		for _, s := range sloStates {
			b.WriteString(fmt.Sprintf("code_agent_slo_budget_burn{slo=\"%s\",status=\"%s\"} %.1f\n",
				s.Name, s.Status, s.ErrorBudgetBurn))
		}
	}

	// Per-server circuit breaker state gauges.
	if metrics, ok := Current().(*Metrics); ok {
		states := metrics.CircuitBreakerStates()
		if len(states) > 0 {
			b.WriteString("# HELP code_agent_circuit_breaker_state Current circuit breaker state per MCP server (0=normal, 1=half_open, 2=open)\n")
			b.WriteString("# TYPE code_agent_circuit_breaker_state gauge\n")
			stateNum := func(s string) int {
				switch s {
				case "half_open":
					return 1
				case "open":
					return 2
				default:
					return 0
				}
			}
			for server, state := range states {
				b.WriteString(fmt.Sprintf("code_agent_circuit_breaker_state{server=\"%s\",state=\"%s\"} %d\n",
					server, state, stateNum(state)))
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if _, err := w.Write([]byte(b.String())); err != nil {
		LogError("prometheus write", err)
	}
}
