package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	sseinfra "github.com/spray272598/code-agent/internal/infrastructure/sse"
	"github.com/spray272598/code-agent/internal/observability"
)

func Build(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}

	b := &builder{cfg: cfg}
	b.wireFoundation()
	// Repos closer must run when Build returns, mirroring the original defer
	// placed right after buildRepos.
	defer func() {
		if b.repos.Closer != nil {
			b.repos.Closer()
		}
	}()
	b.wireTools()
	b.wireMemoryEmbedding()
	b.wireMCP()
	b.wireRunner()
	return b.wireChat(), nil
}

func init() {
	observability.RegisterSSESnapshot(func() []observability.SSECounter {
		return []observability.SSECounter{
			{Name: "code_agent_sse_active_connections", Help: "Currently active SSE connections", Type: "gauge", Value: sseinfra.SSEActiveConnections()},
			{Name: "code_agent_sse_total_connections", Help: "Total SSE connections since start", Type: "counter", Value: sseinfra.SSETotalConnections()},
			{Name: "code_agent_sse_total_events", Help: "Total SSE events emitted", Type: "counter", Value: sseinfra.SSETotalEvents()},
			{Name: "code_agent_sse_total_bytes", Help: "Total SSE bytes sent", Type: "counter", Value: sseinfra.SSETotalBytes()},
			{Name: "code_agent_sse_total_dropped", Help: "Total SSE events dropped due to backpressure", Type: "counter", Value: sseinfra.SSETotalDropped()},
			{Name: "code_agent_sse_heartbeats_sent", Help: "Total SSE heartbeats sent", Type: "counter", Value: sseinfra.SSEHeartbeatsSent()},
			{Name: "code_agent_sse_doom_loop_detected", Help: "Total SSE doom loop detections", Type: "counter", Value: sseinfra.SSEDoomLoopDetected()},
		}
	})
}

func findMCPDemo() string {
	cands := []string{"./mcp-demo", "./mcp-demo.exe", "./bin/mcp-demo", "./bin/mcp-demo.exe"}
	if ex, err := os.Executable(); err == nil {
		dir := filepath.Dir(ex)
		cands = append(cands, filepath.Join(dir, "mcp-demo"), filepath.Join(dir, "mcp-demo.exe"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}
