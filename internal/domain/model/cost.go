package model

import (
	"sync"
)

// CostTracker estimates and accumulates LLM spend, enforcing a per-session or
// per-tenant budget. Prices come from the selected ModelRoute.
type CostTracker struct {
	mu        sync.Mutex
	budgetUSD float64
	spentUSD  float64
}

// NewCostTracker creates a tracker with an optional USD budget (<=0 = unlimited).
func NewCostTracker(budgetUSD float64) *CostTracker {
	return &CostTracker{budgetUSD: budgetUSD}
}

// EstimateUSD returns the estimated cost for a call given token counts and a
// route's per-1k prices.
func EstimateUSD(inTok, outTok int, r ModelRoute) float64 {
	return float64(inTok)/1000*r.CostPer1kIn + float64(outTok)/1000*r.CostPer1kOut
}

// Add records spend for a completed call and reports the new cumulative total.
func (c *CostTracker) Add(usd float64) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spentUSD += usd
	return c.spentUSD
}

// Spent returns cumulative spend.
func (c *CostTracker) Spent() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spentUSD
}

// Budget returns the configured budget.
func (c *CostTracker) Budget() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.budgetUSD
}

// OverBudget reports whether cumulative spend has exceeded the budget.
// A non-positive budget is treated as unlimited (never over).
func (c *CostTracker) OverBudget() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budgetUSD <= 0 {
		return false
	}
	return c.spentUSD >= c.budgetUSD
}

// Remaining returns budget - spent (<=0 if over or unlimited returns budget).
func (c *CostTracker) Remaining() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budgetUSD <= 0 {
		return -1 // unlimited
	}
	return c.budgetUSD - c.spentUSD
}
