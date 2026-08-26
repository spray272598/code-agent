package einoorch

import "sync/atomic"

// BudgetManager enforces token and agent quotas for multi-agent orchestration (P0-2).
// It is safe for concurrent use by parallel sub-agents.
type BudgetManager struct {
	maxTokens  int64
	usedTokens int64
	maxAgents  int64
	usedAgents int64
	epoch      int64 // generation counter; stale results from superseded runs are discarded
}

// NewBudgetManager returns a BudgetManager with given quotas.
func NewBudgetManager(maxTokens, maxAgents int) *BudgetManager {
	return &BudgetManager{
		maxTokens: int64(maxTokens),
		maxAgents: int64(maxAgents),
	}
}

// TryReserveAgents attempts to reserve n agent slots. Returns false when budget is exhausted.
func (b *BudgetManager) TryReserveAgents(n int) bool {
	if b == nil || n <= 0 {
		return true
	}
	old := atomic.LoadInt64(&b.usedAgents)
	for {
		if old+int64(n) > b.maxAgents {
			return false
		}
		if atomic.CompareAndSwapInt64(&b.usedAgents, old, old+int64(n)) {
			return true
		}
		old = atomic.LoadInt64(&b.usedAgents)
	}
}

// ReleaseAgents returns n agent slots to the pool.
func (b *BudgetManager) ReleaseAgents(n int) {
	if b == nil || n <= 0 {
		return
	}
	atomic.AddInt64(&b.usedAgents, -int64(n))
}

// ConsumeTokens attempts to add n tokens to the running total. Returns false if the quota would be exceeded.
func (b *BudgetManager) ConsumeTokens(n int) bool {
	if b == nil || n <= 0 {
		return true
	}
	old := atomic.LoadInt64(&b.usedTokens)
	for {
		if old+int64(n) > b.maxTokens {
			return false
		}
		if atomic.CompareAndSwapInt64(&b.usedTokens, old, old+int64(n)) {
			return true
		}
		old = atomic.LoadInt64(&b.usedTokens)
	}
}

// TokensUsed returns currently consumed tokens (atomic read).
func (b *BudgetManager) TokensUsed() int64 {
	if b == nil {
		return 0
	}
	return atomic.LoadInt64(&b.usedTokens)
}

// RemainingAgents returns free agent slots.
func (b *BudgetManager) RemainingAgents() int64 {
	if b == nil {
		return 0
	}
	used := atomic.LoadInt64(&b.usedAgents)
	return b.maxAgents - used
}

// RemainingTokens returns free token slots.
func (b *BudgetManager) RemainingTokens() int64 {
	if b == nil {
		return 0
	}
	used := atomic.LoadInt64(&b.usedTokens)
	return b.maxTokens - used
}

// MaxAgents returns the configured agent quota.
func (b *BudgetManager) MaxAgents() int64 {
	if b == nil {
		return 0
	}
	return b.maxAgents
}

// MaxTokens returns the configured token quota.
func (b *BudgetManager) MaxTokens() int64 {
	if b == nil {
		return 0
	}
	return b.maxTokens
}

// NextEpoch bumps the generation counter; callers can use this to detect stale results from superseded runs.
func (b *BudgetManager) NextEpoch() int64 {
	if b == nil {
		return 0
	}
	return atomic.AddInt64(&b.epoch, 1)
}

// Epoch returns the current generation counter.
func (b *BudgetManager) Epoch() int64 {
	if b == nil {
		return 0
	}
	return atomic.LoadInt64(&b.epoch)
}
