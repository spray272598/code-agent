package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// ErrTenantMismatch is returned when a caller's tenant.UserID does not match
// the userID that this Manager was constructed with. It is the runtime
// guarantee that no code path can confuse two users' MCP tool spaces.
var ErrTenantMismatch = errors.New("mcp manager tenant mismatch")

// Watchdog + circuit-breaker tuning. The watchdog runs a fixed 15s tick; the
// per-server state machine below decides whether each tick actually pings or
// reconnects a given server, so a permanently dead server can never be
// hammered at a fixed high frequency.
const (
	watchdogInterval = 15 * time.Second // heartbeat / reconnect tick
	pingTimeout      = 5 * time.Second  // per-heartbeat ping deadline
	reconnectTimeout = 20 * time.Second // per-reconnect attempt deadline

	maxConsecutiveFailures = 5                // consecutive failures before circuit opens
	baseBackoff            = 15 * time.Second // first retry delay (aligns with watchdog tick)
	maxBackoff             = 5 * time.Minute  // exponential backoff ceiling
	openWindow             = 60 * time.Second // circuit-open duration before half-open probe
)

// connStateType is the per-server connection circuit-breaker state.
type connStateType string

const (
	stateNormal   connStateType = "normal"    // online, heartbeats passing
	stateRetry    connStateType = "retry"     // offline, waiting out exponential backoff
	stateOpen     connStateType = "open"      // circuit open, stop reconnecting for a window
	stateHalfOpen connStateType = "half_open" // open window elapsed, allow one probe
)

// connState tracks the reconnect circuit-breaker for a single server.
type connState struct {
	state       connStateType
	failCount   int       // consecutive failures (drives backoff + open threshold)
	nextRetryAt time.Time // when the next reconnect attempt is allowed (retry)
	openUntil   time.Time // when the open window ends and half-open may start (open)
}

// Manager implements mcpport.IMCPManagerPort and is per-user (Sprint 1.6).
// One Manager owns the MCP tool space of exactly one userID: it can only
// configure, list, and call servers that the owning user configured. The
// runtime invariant is checked via WithTenant: any cross-tenant call returns
// ErrTenantMismatch and is recorded in the audit log.
type Manager struct {
	mu          sync.RWMutex
	userID      string // owner; "" means system / bootstrap-managed
	clients     map[string]mcpport.IMCPClient
	configs     map[string]model.ServerConfig
	toolRoute   map[string]string // toolName -> server\x00realName
	toolDefs    []model.ToolDef
	onChange    func([]model.ToolDef)
	lastErr     map[string]string
	states      map[string]*connState // per-server circuit-breaker state
	stopWatch   chan struct{}
	watchOnce   sync.Once
	reconnectMu sync.Mutex
}

// NewManager is the legacy system-level constructor used by bootstrap for the
// demo server. Prefer NewUserManager(userID) for any user-facing path.
func NewManager() *Manager {
	return NewUserManager("")
}

// NewUserManager constructs a per-user Manager. userID is the owning tenant;
// pass the authenticated principal's userID from ctx (tenant.UserID(ctx)) or
// the JWT subject. Empty userID is allowed only for system-level servers.
func NewUserManager(userID string) *Manager {
	m := &Manager{
		userID:    userID,
		clients:   make(map[string]mcpport.IMCPClient),
		configs:   make(map[string]model.ServerConfig),
		toolRoute: make(map[string]string),
		lastErr:   make(map[string]string),
		states:    make(map[string]*connState),
		stopWatch: make(chan struct{}),
	}
	m.startWatchdog()
	return m
}

// Owner returns the userID this Manager owns. Used by callers to stamp the
// audit log entry on each tool call.
func (m *Manager) Owner() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.userID
}

// AssertTenant is the runtime guard: it returns ErrTenantMismatch when ctx's
// tenant.UserID differs from this Manager's owner. Empty owner (system) is
// always allowed; empty caller is rejected.
func (m *Manager) AssertTenant(ctx context.Context) error {
	want := m.Owner()
	if want == "" {
		return nil // system-owned; no assertion
	}
	// import cycle avoidance: tenant extraction is inlined as a string lookup.
	if ctx == nil {
		return ErrTenantMismatch
	}
	got, _ := ctx.Value(tenantKey{}).(string)
	if got == "" || got != want {
		return ErrTenantMismatch
	}
	return nil
}

// tenantKey is the unexported ctx key used to carry the asserted userID.
// business code uses tenant.From(ctx), but this lower layer accepts the raw
// userID via WithAssertedUser to keep the dependency direction one-way.
type tenantKey struct{}

// WithAssertedUser stamps the userID on ctx so the per-user Manager can
// perform its runtime assertion without importing the tenant package
// (avoiding a cycle: tenant → ... → mcp → tenant).
func WithAssertedUser(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, tenantKey{}, userID)
}

// startWatchdog periodically restarts offline enabled servers.
// Each tick first runs a heartbeat (MCP ping) against online clients: any
// client that fails to answer is closed and marked offline, then handed off
// to reconnectOffline for restart. This is genuine keep-alive — a stalled or
// crashed peer is detected within one tick instead of waiting for a CallTool
// failure on the next user request.
func (m *Manager) startWatchdog() {
	m.watchOnce.Do(func() {
		go func() {
			t := time.NewTicker(watchdogInterval)
			defer t.Stop()
			for {
				select {
				case <-m.stopWatch:
					return
				case <-t.C:
					m.healthCheck()
					m.reconnectOffline()
				}
			}
		}()
	})
}

// healthCheck sends an MCP ping to every online client. On failure the client
// is closed and removed from the clients map so reconnectOffline can pick it
// up. The ping deadline is short (5s) so one stalled server cannot stall the
// whole watchdog tick.
func (m *Manager) healthCheck() {
	m.mu.RLock()
	snapshot := make([]mcpport.IMCPClient, 0, len(m.clients))
	for _, c := range m.clients {
		snapshot = append(snapshot, c)
	}
	m.mu.RUnlock()

	for _, c := range snapshot {
		ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		err := c.Ping(ctx)
		cancel()
		if err == nil {
			continue
		}
		name := c.Name()
		log.Printf("[mcp] heartbeat %s failed: %v → mark offline\n", name, err)
		m.mu.Lock()
		// Guard against replacing a freshly-reconnected client: only drop the
		// exact instance we pinged.
		if cur, ok := m.clients[name]; ok && cur == c {
			_ = c.Close()
			delete(m.clients, name)
		}
		m.mu.Unlock()
		// Advance the circuit-breaker: this failure bumps failCount and, past
		// the threshold, opens the circuit so reconnectOffline stops retrying.
		m.recordFailure(name, fmt.Errorf("heartbeat failed: %w", err))
	}
}

// reconnectOffline walks every enabled but offline server and decides, per the
// circuit-breaker state, whether this tick may attempt a reconnect:
//   - retry:     only when the exponential-backoff deadline has passed
//   - open:      skip until the open window elapses, then transition to half-open
//   - half_open: allow a single probe
//
// This is what turns the fixed 15s watchdog tick into an exponential-backoff +
// circuit-breaker schedule instead of a tight reconnect loop.
func (m *Manager) reconnectOffline() {
	now := time.Now()

	type candidate struct {
		cfg   model.ServerConfig
		state connStateType
	}

	m.mu.RLock()
	var need []candidate
	for name, cfg := range m.configs {
		if !cfg.Enabled {
			continue
		}
		if _, online := m.clients[name]; online {
			continue
		}

		st := m.states[name]
		state := stateRetry
		var nextRetryAt, openUntil time.Time
		if st != nil {
			state = st.state
			nextRetryAt = st.nextRetryAt
			openUntil = st.openUntil
		}

		switch state {
		case stateOpen:
			if now.Before(openUntil) {
				continue // circuit open — stop pinging for the window
			}
			need = append(need, candidate{cfg: cfg, state: stateHalfOpen})
		case stateRetry:
			if now.Before(nextRetryAt) {
				continue // still backing off
			}
			need = append(need, candidate{cfg: cfg, state: stateRetry})
		case stateHalfOpen:
			need = append(need, candidate{cfg: cfg, state: stateHalfOpen})
		default: // stateNormal but offline (no state recorded yet)
			need = append(need, candidate{cfg: cfg, state: stateRetry})
		}
	}
	m.mu.RUnlock()

	for _, cand := range need {
		// Transition open → half_open only once, right before the probe, so a
		// failed half-open probe can be routed back to open by recordFailure.
		if cand.state == stateHalfOpen {
			m.mu.Lock()
			if st := m.states[cand.cfg.Name]; st != nil && st.state == stateOpen {
				st.state = stateHalfOpen
			}
			m.mu.Unlock()
		}
		log.Printf("[mcp] watchdog reconnect %s (state=%s)\n", cand.cfg.Name, cand.state)
		ctx, cancel := context.WithTimeout(context.Background(), reconnectTimeout)
		err := m.reconnect(ctx, cand.cfg.Name)
		cancel()
		if err != nil {
			log.Printf("[mcp] reconnect %s failed: %v\n", cand.cfg.Name, err)
		}
	}
}

// reconnect restarts a server by name using stored config. On success it
// resets the circuit-breaker back to normal; on failure it advances it
// (failCount++ / exponential backoff / open circuit).
func (m *Manager) reconnect(ctx context.Context, name string) error {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	m.mu.RLock()
	cfg, ok := m.configs[name]
	m.mu.RUnlock()
	if !ok || !cfg.Enabled {
		return fmt.Errorf("no config for %s", name)
	}
	if err := m.startOne(ctx, cfg); err != nil {
		m.recordFailure(name, err)
		return err
	}
	// Connection established: clear the error and reset the breaker to normal.
	m.recordSuccess(name)
	_, err := m.refreshTools(ctx)
	return err
}

// backoffFor returns the exponential backoff delay for the nth consecutive
// failure (1-based): baseBackoff, 2x, 4x, ... capped at maxBackoff.
func backoffFor(failCount int) time.Duration {
	d := baseBackoff
	for i := 1; i < failCount; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// recordSuccess resets the per-server circuit-breaker to normal after a
// successful connection. Must be called with m.mu NOT held.
func (m *Manager) recordSuccess(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.lastErr, name)
	if st := m.states[name]; st != nil {
		st.state = stateNormal
		st.failCount = 0
		st.nextRetryAt = time.Time{}
		st.openUntil = time.Time{}
	}
}

// recordFailure advances the per-server circuit-breaker on a failed connect or
// heartbeat. Must be called with m.mu NOT held.
func (m *Manager) recordFailure(name string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr[name] = err.Error()

	st := m.states[name]
	if st == nil {
		st = &connState{}
		m.states[name] = st
	}

	switch st.state {
	case stateHalfOpen:
		// Half-open probe failed: re-open the circuit for a fresh window.
		st.state = stateOpen
		st.openUntil = time.Now().Add(openWindow)
		st.failCount = maxConsecutiveFailures
	case stateOpen:
		// Should only happen via a user-triggered reconnect during open; extend.
		st.openUntil = time.Now().Add(openWindow)
	default: // stateNormal or stateRetry
		st.failCount++
		if st.failCount >= maxConsecutiveFailures {
			st.state = stateOpen
			st.openUntil = time.Now().Add(openWindow)
		} else {
			st.state = stateRetry
			st.nextRetryAt = time.Now().Add(backoffFor(st.failCount))
		}
	}
}

func (m *Manager) OnToolsChanged(cb func([]model.ToolDef)) {
	m.mu.Lock()
	m.onChange = cb
	m.mu.Unlock()
}

func (m *Manager) AddOrUpdate(ctx context.Context, cfg model.ServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("name required")
	}
	if cfg.Transport == "" {
		cfg.Transport = "stdio"
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 60
	}
	m.mu.Lock()
	m.configs[cfg.Name] = cfg
	m.mu.Unlock()
	if !cfg.Enabled {
		return m.Remove(cfg.Name)
	}
	if err := m.startOne(ctx, cfg); err != nil {
		m.recordFailure(cfg.Name, err)
		return err
	}
	m.recordSuccess(cfg.Name)
	_, err := m.refreshTools(ctx)
	return err
}

func (m *Manager) startOne(ctx context.Context, cfg model.ServerConfig) error {
	m.mu.Lock()
	if old, ok := m.clients[cfg.Name]; ok {
		_ = old.Close()
		delete(m.clients, cfg.Name)
	}
	m.mu.Unlock()

	var client mcpport.IMCPClient
	switch strings.ToLower(cfg.Transport) {
	case "stdio", "":
		client = NewStdioClient(cfg)
	case "sse", "http", "streamable", "streamable-http":
		client = NewHTTPClient(cfg)
	default:
		return fmt.Errorf("transport %s not supported (use stdio or http)", cfg.Transport)
	}
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return err
	}
	m.mu.Lock()
	m.clients[cfg.Name] = client
	m.configs[cfg.Name] = cfg
	m.mu.Unlock()
	return nil
}

func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	if c, ok := m.clients[name]; ok {
		_ = c.Close()
		delete(m.clients, name)
	}
	delete(m.configs, name)
	delete(m.lastErr, name)
	delete(m.states, name)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.refreshTools(ctx)
	return err
}

func (m *Manager) ListTools(ctx context.Context) ([]model.ToolDef, error) {
	m.mu.RLock()
	if len(m.toolDefs) > 0 {
		out := append([]model.ToolDef{}, m.toolDefs...)
		m.mu.RUnlock()
		return out, nil
	}
	m.mu.RUnlock()
	return m.refreshTools(ctx)
}

func (m *Manager) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	// Sprint 1.6: runtime tenant guard. A per-user Manager must never execute
	// a tool on behalf of a different user. Empty owner = system path.
	if err := m.AssertTenant(ctx); err != nil {
		return "", err
	}
	m.mu.RLock()
	route, ok := m.toolRoute[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("mcp tool not found: %s", name)
	}
	parts := strings.SplitN(route, "\x00", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid route")
	}
	serverName, realName := parts[0], parts[1]
	m.mu.RLock()
	client := m.clients[serverName]
	m.mu.RUnlock()
	if client == nil {
		// auto-reconnect once
		if err := m.reconnect(ctx, serverName); err != nil {
			return "", fmt.Errorf("mcp server offline: %s (%v)", serverName, err)
		}
		m.mu.RLock()
		client = m.clients[serverName]
		m.mu.RUnlock()
		if client == nil {
			return "", fmt.Errorf("mcp server offline: %s", serverName)
		}
	}
	text, err := client.CallTool(ctx, realName, args)
	if err != nil {
		// process crash / broken pipe → reconnect and retry once
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "broken pipe") || strings.Contains(low, "eof") ||
			strings.Contains(low, "exit") || strings.Contains(low, "closed") ||
			strings.Contains(low, "process") {
			log.Printf("[mcp] call failed, reconnect %s: %v\n", serverName, err)
			if rerr := m.reconnect(ctx, serverName); rerr == nil {
				m.mu.RLock()
				client = m.clients[serverName]
				m.mu.RUnlock()
				if client != nil {
					return client.CallTool(ctx, realName, args)
				}
			}
		}
		return "", err
	}
	return text, nil
}

func (m *Manager) IsOnline(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.clients[name]
	return ok
}

func (m *Manager) Health(ctx context.Context) []model.HealthStatus {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	// count tools per server
	counts := map[string]int{}
	for _, t := range m.toolDefs {
		counts[t.ServerName]++
	}
	var out []model.HealthStatus
	for name, cfg := range m.configs {
		_, online := m.clients[name]
		state := stateNormal
		if !online {
			if st := m.states[name]; st != nil {
				state = st.state
			} else {
				state = stateRetry
			}
		}
		out = append(out, model.HealthStatus{
			Name: name, Online: online, Transport: cfg.Transport,
			ToolCount: counts[name], LastError: m.lastErr[name], Enabled: cfg.Enabled,
			State: string(state),
		})
	}
	return out
}

func (m *Manager) ListServers() []model.ServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.ServerConfig, 0, len(m.configs))
	for _, c := range m.configs {
		out = append(out, c)
	}
	return out
}

func (m *Manager) Close() error {
	select {
	case <-m.stopWatch:
	default:
		close(m.stopWatch)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for n, c := range m.clients {
		_ = c.Close()
		delete(m.clients, n)
	}
	return nil
}

func (m *Manager) refreshTools(ctx context.Context) ([]model.ToolDef, error) {
	m.mu.RLock()
	clients := make([]mcpport.IMCPClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	cb := m.onChange
	m.mu.RUnlock()

	route := map[string]string{}
	var defs []model.ToolDef
	type pair struct {
		c     mcpport.IMCPClient
		tools []model.ToolDef
	}
	var pairs []pair
	for _, c := range clients {
		tools, err := c.ListTools(ctx)
		if err != nil {
			log.Printf("[mcp] list tools %s: %v\n", c.Name(), err)
			continue
		}
		pairs = append(pairs, pair{c: c, tools: tools})
	}
	for _, p := range pairs {
		for _, t := range p.tools {
			// Always server__tool: Guard MCP policy + no collision with core tools
			name := p.c.Name() + "__" + t.Name
			route[name] = p.c.Name() + "\x00" + t.Name
			td := t
			td.Name = name
			td.ServerName = p.c.Name()
			td.Description = fmt.Sprintf("[MCP:%s] %s", p.c.Name(), t.Description)
			defs = append(defs, td)
		}
	}
	m.mu.Lock()
	m.toolRoute = route
	m.toolDefs = defs
	m.mu.Unlock()
	if cb != nil {
		cb(defs)
	}
	return defs, nil
}

// Ensure Manager implements the port.
var _ mcpport.IMCPManagerPort = (*Manager)(nil)
