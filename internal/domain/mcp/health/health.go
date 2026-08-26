package mcp

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
)

// CircuitBreaker implements a simple 3-state circuit breaker for each MCP
// server: normal → half-open → open. When a server fails consecutively,
// calls are short-circuited for a cooldown period to avoid cascading failures.
type CircuitBreaker struct {
	mu            sync.Mutex
	state         string // normal | half_open | open
	failures      int
	successes     int
	lastFailure   time.Time
	openedAt      time.Time
	failureThresh int
	cooldown      time.Duration
	serverName    string
}

const (
	StateNormal             = "normal"
	StateHalfOpen           = "half_open"
	StateOpen               = "open"
	DefaultFailureThreshold = 3
	DefaultCooldown         = 30 * time.Second
)

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:         StateNormal,
		failureThresh: DefaultFailureThreshold,
		cooldown:      DefaultCooldown,
	}
}

// NewCircuitBreakerFor creates a circuit breaker for a specific server.
func NewCircuitBreakerFor(serverName string) *CircuitBreaker {
	return &CircuitBreaker{
		state:         StateNormal,
		failureThresh: DefaultFailureThreshold,
		cooldown:      DefaultCooldown,
		serverName:    serverName,
	}
}

// Allow returns true if a call should be attempted. If the circuit is open
// and cooldown has elapsed, it transitions to half-open and permits one probe.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.cooldown {
			cb.state = StateHalfOpen
			telemetry.IncCircuitBreakerStateTransition()
			telemetry.SetCircuitBreakerState(cb.serverName, StateHalfOpen)
			telemetry.TraceEvent(map[string]any{
				"event":  "circuit_breaker_transition",
				"server": cb.serverName,
				"from":   StateOpen,
				"to":     StateHalfOpen,
			})
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return true
	}
}

// RecordSuccess transitions back to normal and resets the failure counter.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	prev := cb.state
	cb.state = StateNormal
	cb.failures = 0
	cb.successes++
	if prev != StateNormal {
		telemetry.IncCircuitBreakerStateTransition()
		telemetry.SetCircuitBreakerState(cb.serverName, StateNormal)
		telemetry.TraceEvent(map[string]any{
			"event":  "circuit_breaker_transition",
			"server": cb.serverName,
			"from":   prev,
			"to":     StateNormal,
		})
	}
}

// RecordFailure increments the failure counter and opens the circuit when
// the threshold is exceeded.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.state == StateHalfOpen || cb.failures >= cb.failureThresh {
		prev := cb.state
		cb.state = StateOpen
		cb.openedAt = time.Now()
		telemetry.IncCircuitBreakerStateTransition()
		telemetry.SetCircuitBreakerState(cb.serverName, StateOpen)
		telemetry.TraceEvent(map[string]any{
			"event":    "circuit_breaker_transition",
			"server":   cb.serverName,
			"from":     prev,
			"to":       StateOpen,
			"failures": cb.failures,
		})
		log.Printf("[mcp-cb] circuit opened for %s after %d failures\n", cb.serverName, cb.failures)
	}
}

// State returns the current state.
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// MCPHealthMonitor provides per-server health snapshots and supports
// background health checks (PING) for registered MCP servers.
type MCPHealthMonitor struct {
	mu       sync.RWMutex
	servers  map[string]*serverHealth
	checkFn  func(ctx context.Context, serverName string) error
	interval time.Duration
	stopCh   chan struct{}
}

type serverHealth struct {
	def       model.ServerConfig
	cb        *CircuitBreaker
	lastPing  time.Time
	lastError string
	online    bool
	toolCount int
}

func NewMCPHealthMonitor(interval time.Duration, checkFn func(ctx context.Context, name string) error) *MCPHealthMonitor {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &MCPHealthMonitor{
		servers:  map[string]*serverHealth{},
		checkFn:  checkFn,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// RegisterServer adds a server to be monitored.
func (m *MCPHealthMonitor) RegisterServer(cfg model.ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[cfg.Name] = &serverHealth{
		def:    cfg,
		cb:     NewCircuitBreakerFor(cfg.Name),
		online: cfg.Enabled,
	}
}

// RemoveServer stops monitoring a server.
func (m *MCPHealthMonitor) RemoveServer(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.servers, name)
}

// GetServer returns the circuit breaker for a server (for use by callers).
func (m *MCPHealthMonitor) GetServer(name string) *CircuitBreaker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.servers[name]; ok {
		return s.cb
	}
	return nil
}

// UpdateToolCount updates the cached tool count for a server (used by bridge after Sync).
func (m *MCPHealthMonitor) UpdateToolCount(name string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.servers[name]; ok {
		s.toolCount = count
	}
}

// Start begins background health checks. Blocks until Stop is called.
func (m *MCPHealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.runChecks(ctx)
		}
	}
}

// Stop terminates the background health check loop.
func (m *MCPHealthMonitor) Stop() {
	close(m.stopCh)
}

func (m *MCPHealthMonitor) runChecks(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		cb := m.GetServer(name)
		if cb == nil || !cb.Allow() {
			continue
		}
		if m.checkFn == nil {
			continue
		}
		err := m.checkFn(ctx, name)
		m.mu.Lock()
		if s, ok := m.servers[name]; ok {
			s.lastPing = time.Now()
			if err != nil {
				s.online = false
				s.lastError = err.Error()
				cb.RecordFailure()
			} else {
				s.online = true
				s.lastError = ""
				cb.RecordSuccess()
			}
		}
		m.mu.Unlock()
	}
}

// Snapshot returns per-server HealthStatus for observability.
func (m *MCPHealthMonitor) Snapshot() []model.HealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.HealthStatus, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, model.HealthStatus{
			Name:      s.def.Name,
			Online:    s.online,
			Transport: s.def.Transport,
			ToolCount: s.toolCount,
			LastError: s.lastError,
			Enabled:   s.def.Enabled,
			State:     s.cb.State(),
		})
	}
	return out
}

// SnapshotMap returns per-server health as []map[string]any for cross-package
// integration (e.g. host.HeartbeatManager).
func (m *MCPHealthMonitor) SnapshotMap() []map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]map[string]any, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, map[string]any{
			"name":       s.def.Name,
			"online":     s.online,
			"transport":  s.def.Transport,
			"tool_count": s.toolCount,
			"last_error": s.lastError,
			"enabled":    s.def.Enabled,
			"state":      s.cb.State(),
		})
	}
	return out
}

// FormatStatus is a human-readable summary string (for logging / debug).
func (m *MCPHealthMonitor) FormatStatus() string {
	snap := m.Snapshot()
	var b []byte
	for _, s := range snap {
		b = append(b, []byte(fmt.Sprintf("[%s] online=%v tools=%d state=%s err=%q\n",
			s.Name, s.Online, s.ToolCount, s.State, s.LastError))...)
	}
	return string(b)
}
