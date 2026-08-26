package observability

import (
	"sync"
	"time"
)

// SLO defines a Service Level Objective.
type SLO struct {
	Name            string        `json:"name"`
	TargetLatency   time.Duration `json:"targetLatency"`   // e.g. 99% of requests < 2s
	TargetErrorRate float64       `json:"targetErrorRate"` // e.g. < 1% error rate
	Window          time.Duration `json:"window"`          // evaluation window (e.g. 1h)
}

// SLOState tracks the current state of an SLO.
type SLOState struct {
	Name            string  `json:"name"`
	LatencyP50Ms    int64   `json:"latencyP50Ms"`
	LatencyP95Ms    int64   `json:"latencyP95Ms"`
	LatencyP99Ms    int64   `json:"latencyP99Ms"`
	ErrorRate       float64 `json:"errorRate"`
	Availability    float64 `json:"availability"`
	ErrorBudgetBurn float64 `json:"errorBudgetBurn"` // 0-100 (how much of budget consumed)
	Status          string  `json:"status"`          // "healthy", "degraded", "exhausted"
}

// SLOMonitor tracks latency, errors, and availability for SLO compliance.
type SLOMonitor struct {
	mu      sync.Mutex
	slos    map[string]SLO
	latency *latencyTracker
	errors  *errorTracker
}

// NewSLOMonitor creates an SLO monitor with default SLIs.
func NewSLOMonitor() *SLOMonitor {
	m := &SLOMonitor{
		slos: map[string]SLO{},
	}
	// Register default SLIs.
	m.RegisterSLO(SLO{
		Name:            "agent_latency",
		TargetLatency:   30 * time.Second,
		TargetErrorRate: 0.02,
		Window:          1 * time.Hour,
	})
	m.RegisterSLO(SLO{
		Name:            "tool_call_latency",
		TargetLatency:   5 * time.Second,
		TargetErrorRate: 0.05,
		Window:          1 * time.Hour,
	})
	m.RegisterSLO(SLO{
		Name:            "llm_call_latency",
		TargetLatency:   10 * time.Second,
		TargetErrorRate: 0.03,
		Window:          1 * time.Hour,
	})
	m.latency = newLatencyTracker(10000) // keep last 10k samples
	m.errors = newErrorTracker()
	return m
}

// RegisterSLO adds or updates an SLO definition.
func (m *SLOMonitor) RegisterSLO(s SLO) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slos[s.Name] = s
}

// RecordLatency records a latency sample for the given SLO.
func (m *SLOMonitor) RecordLatency(sloName string, d time.Duration, err error) {
	m.latency.record(sloName, d)
	if err != nil {
		m.errors.record(sloName)
	}
}

// RecordError records an error for the given SLO.
func (m *SLOMonitor) RecordError(sloName string) {
	m.errors.record(sloName)
}

// Evaluate returns the current SLO state for the given SLO name.
func (m *SLOMonitor) Evaluate(sloName string) SLOState {
	m.mu.Lock()
	slo, ok := m.slos[sloName]
	m.mu.Unlock()
	if !ok {
		return SLOState{Name: sloName, Status: "unknown"}
	}

	p50, p95, p99 := m.latency.percentiles(sloName)
	errorRate, totalRequests := m.errors.errorRate(sloName)
	availability := 1.0 - errorRate

	state := SLOState{
		Name:         sloName,
		LatencyP50Ms: p50,
		LatencyP95Ms: p95,
		LatencyP99Ms: p99,
		ErrorRate:    errorRate,
		Availability: availability,
	}

	// Error budget: 50% of budget = 2x error rate, 100% = 4x error rate.
	if slo.TargetErrorRate > 0 {
		state.ErrorBudgetBurn = errorRate / slo.TargetErrorRate * 100
	}

	// Determine status.
	targetMs := slo.TargetLatency.Milliseconds()
	switch {
	case state.ErrorBudgetBurn >= 100:
		state.Status = "exhausted"
	case state.LatencyP99Ms > targetMs || state.ErrorBudgetBurn >= 50:
		state.Status = "degraded"
	default:
		state.Status = "healthy"
	}
	_ = totalRequests
	return state
}

// AllStates returns the current state of all registered SLIs.
func (m *SLOMonitor) AllStates() []SLOState {
	m.mu.Lock()
	defer m.mu.Unlock()
	var states []SLOState
	for name := range m.slos {
		states = append(states, m.Evaluate(name))
	}
	return states
}

// GlobalSLO is the process-wide SLO monitor.
var GlobalSLO = NewSLOMonitor()

// --- latencyTracker: streaming percentile approximation ---

type latencyTracker struct {
	mu      sync.Mutex
	samples map[string][]int64 // sloName → latencies in ms
	maxSize int
}

func newLatencyTracker(maxSize int) *latencyTracker {
	return &latencyTracker{
		samples: map[string][]int64{},
		maxSize: maxSize,
	}
}

func (t *latencyTracker) record(sloName string, d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ms := d.Milliseconds()
	s := t.samples[sloName]
	s = append(s, ms)
	if len(s) > t.maxSize {
		s = s[len(s)-t.maxSize:]
	}
	t.samples[sloName] = s
}

// percentiles returns P50, P95, P99 for the given SLO using sorted data.
func (t *latencyTracker) percentiles(sloName string) (p50, p95, p99 int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.samples[sloName]
	if !ok || len(s) == 0 {
		return 0, 0, 0
	}
	// Sort a copy to avoid mutating the original slice.
	sorted := make([]int64, len(s))
	copy(sorted, s)
	sortInt64s(sorted)
	n := int64(len(sorted))
	p50 = percentileFromSorted(sorted, n, 0.50)
	p95 = percentileFromSorted(sorted, n, 0.95)
	p99 = percentileFromSorted(sorted, n, 0.99)
	return p50, p95, p99
}

// percentileFromSorted returns the value at the given percentile from a sorted slice.
func percentileFromSorted(sorted []int64, n int64, p float64) int64 {
	if n == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1.0 {
		return sorted[n-1]
	}
	// Use linear interpolation between closest ranks.
	rank := p * float64(n-1)
	lower := int64(rank)
	upper := lower + 1
	if upper >= n {
		return sorted[n-1]
	}
	frac := rank - float64(lower)
	return sorted[lower] + int64(float64(sorted[upper]-sorted[lower])*frac)
}

// sortInt64s sorts a slice of int64s in-place (simple insertion sort for small slices,
// falling back to quicksort-like approach for larger ones).
func sortInt64s(s []int64) {
	if len(s) <= 1 {
		return
	}
	// Use a simple sort for correctness; the slices are typically < 10k entries.
	// Insertion sort for small slices, quicksort for larger.
	if len(s) < 16 {
		for i := 1; i < len(s); i++ {
			key := s[i]
			j := i - 1
			for j >= 0 && s[j] > key {
				s[j+1] = s[j]
				j--
			}
			s[j+1] = key
		}
		return
	}
	// Quicksort for larger slices.
	quickSortInt64s(s, 0, len(s)-1)
}

func quickSortInt64s(s []int64, lo, hi int) {
	if lo >= hi {
		return
	}
	pivot := s[hi]
	i := lo - 1
	for j := lo; j < hi; j++ {
		if s[j] <= pivot {
			i++
			s[i], s[j] = s[j], s[i]
		}
	}
	s[i+1], s[hi] = s[hi], s[i+1]
	p := i + 1
	quickSortInt64s(s, lo, p-1)
	quickSortInt64s(s, p+1, hi)
}

// --- errorTracker: rolling error rate ---

type errorTracker struct {
	mu      sync.Mutex
	total   map[string]int64 // sloName → total requests
	errored map[string]int64 // sloName → error count
}

func newErrorTracker() *errorTracker {
	return &errorTracker{
		total:   map[string]int64{},
		errored: map[string]int64{},
	}
}

func (e *errorTracker) record(sloName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.total[sloName]++
	e.errored[sloName]++
}

func (e *errorTracker) errorRate(sloName string) (float64, int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	total := e.total[sloName]
	if total == 0 {
		return 0, 0
	}
	return float64(e.errored[sloName]) / float64(total), total
}

// Reset clears all tracked data. Useful for tests.
func (m *SLOMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latency = newLatencyTracker(10000)
	m.errors = newErrorTracker()
}

var (
	_ = GlobalSLO.Evaluate
	_ = func() { GlobalSLO.RecordLatency("test", 100*time.Millisecond, nil) }
)
