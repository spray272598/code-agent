package mcp

import (
	"context"
	"sync"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tenant"
)

// UserFactory caches one *Manager per authenticated userID (Sprint 1.6). The
// cache is the runtime guarantee that there is exactly one tool space per
// user: every request for user A obtains the same Manager that A's previous
// requests configured and populated, and nobody else can ever reach it.
//
// The factory is the ONLY way domain/business code is allowed to obtain a
// Manager. There is no global singleton, no shared default, and no public
// constructor that lets the domain layer build a Manager of arbitrary
// ownership. The bootstrap layer builds the factory and hands it to the
// application layer via dependency injection.
type UserFactory struct {
	mu      sync.RWMutex
	byUser  map[string]*Manager
	build   func(userID string) *Manager
	metric  FactoryMetric // optional observability hook
}

// FactoryMetric captures the per-user create/reuse events so audit/observability
// can verify the factory is actually in use (and that no Manager escaped it).
// Aliased to the port type so *UserFactory satisfies port.IUserMCPManagerFactory.
type FactoryMetric = mcpport.FactoryMetric

// UserFactoryOption configures the factory. build is required (typically a thin
// wrapper that returns mcp.NewUserManager(userID) with optional extra wiring).
type UserFactoryOption func(*UserFactory)

// WithBuilder sets the per-user Manager constructor. Required.
func WithBuilder(build func(userID string) *Manager) UserFactoryOption {
	return func(f *UserFactory) { f.build = build }
}

// NewUserFactory constructs the factory. build must be non-nil.
func NewUserFactory(build func(userID string) *Manager) *UserFactory {
	return &UserFactory{
		byUser: make(map[string]*Manager),
		build:  build,
	}
}

// For returns the Manager for the authenticated user. The userID is taken
// from tenant.From(ctx); missing tenant is a hard error so the caller can't
// silently fall back to a global singleton.
func (f *UserFactory) For(ctx context.Context) (mcpport.IMCPManagerPort, error) {
	if f == nil || f.build == nil {
		return nil, ErrTenantMismatch
	}
	t, ok := tenant.From(ctx)
	if !ok || t.UserID == "" {
		return nil, ErrTenantMismatch
	}
	m, err := f.ForUserID(t.UserID)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ForUserID is the direct path for callers that already know the userID (e.g.
// background tasks, the demo bootstrap). Prefer For(ctx) for request-scoped
// code so the tenant assertion is always performed. The empty userID is
// allowed only when the factory was primed with a system Manager via
// PrimeSystem; otherwise it's a hard error.
func (f *UserFactory) ForUserID(userID string) (mcpport.IMCPManagerPort, error) {
	if f == nil || f.build == nil {
		return nil, ErrTenantMismatch
	}
	if userID == "" {
		// Allow only when a system manager was explicitly primed.
		f.mu.RLock()
		sys, hit := f.byUser[""]
		f.mu.RUnlock()
		if !hit {
			return nil, ErrTenantMismatch
		}
		return sys, nil
	}
	f.mu.RLock()
	m, hit := f.byUser[userID]
	f.mu.RUnlock()
	if hit {
		f.metric.Reused++
		f.metric.Returns++
		return m, nil
	}
	f.mu.Lock()
	m, hit = f.byUser[userID]
	if !hit {
		m = f.build(userID)
		f.byUser[userID] = m
		f.metric.Created++
	}
	f.mu.Unlock()
	f.metric.Returns++
	return m, nil
}

// Metrics returns a snapshot of the factory counters. Cheap, safe for hot paths.
func (f *UserFactory) Metrics() FactoryMetric {
	if f == nil {
		return FactoryMetric{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.metric
}

// ResetAll closes every cached Manager and clears the cache. Intended for
// graceful shutdown or admin "kick everyone off" operations. The factory is
// still usable after a reset — the next For() call simply rebuilds.
func (f *UserFactory) ResetAll() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.byUser {
		_ = m.Close()
	}
	f.byUser = make(map[string]*Manager)
}

// PrimeSystem seeds the factory's cache with a Manager under the "" key so
// the bootstrap can pre-load servers (cfg.MCP.ConfigFile, demo) and have
// downstream code reach them via ForUserID(""). Use this only during
// initialization; calling it at runtime is a configuration error.
func (f *UserFactory) PrimeSystem(sys *Manager) {
	if f == nil || sys == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, hit := f.byUser[""]; hit {
		return // already primed; don't replace
	}
	f.byUser[""] = sys
}

// Count returns the number of cached managers (one per active user).
func (f *UserFactory) Count() int {
	if f == nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.byUser)
}