package security

import (
	"sync"
	"time"
)

// RiskLevel categorizes the adaptive circuit breaker threshold.
type RiskLevel int

const (
	RiskNormal RiskLevel = iota
	RiskElevated
	RiskHigh
	RiskCritical
)

func (r RiskLevel) String() string {
	switch r {
	case RiskNormal:
		return "normal"
	case RiskElevated:
		return "elevated"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// AdaptiveCircuitBreaker dynamically adjusts the denial threshold based on
// session risk factors: sandbox mode, anomaly count, injection detections,
// and history of violations. This prevents attackers from probing the
// security boundary by repeatedly testing tool calls.
type AdaptiveCircuitBreaker struct {
	mu sync.RWMutex

	// Base thresholds per risk level
	baseThresholds map[RiskLevel]int

	// Current thresholds per session
	sessionThresholds map[string]int

	// Denial history per session (with timestamps for decay)
	denialHistory map[string][]denialRecord

	// Risk assessment data sources (set by Guard on initialization)
	getMode        func() SandboxMode
	getRiskLevel   func(sessionID string) BehaviorRisk
	getInjection   func(sessionID string) (totalDetections int)

	// Decay parameters
	decayDuration  time.Duration
	minThreshold   int
	maxThreshold   int
}

type denialRecord struct {
	Time   time.Time
	Reason string
	Tool   string
}

// NewAdaptiveCircuitBreaker creates a circuit breaker with adaptive thresholds.
func NewAdaptiveCircuitBreaker() *AdaptiveCircuitBreaker {
	return &AdaptiveCircuitBreaker{
		baseThresholds: map[RiskLevel]int{
			RiskNormal:    5,
			RiskElevated:  4,
			RiskHigh:      3,
			RiskCritical:  2,
		},
		sessionThresholds: make(map[string]int),
		denialHistory:     make(map[string][]denialRecord),
		decayDuration:     30 * time.Minute,
		minThreshold:      2,
		maxThreshold:      5,
	}
}

// SetRiskSources configures the data sources used for risk assessment.
func (acb *AdaptiveCircuitBreaker) SetRiskSources(
	getMode func() SandboxMode,
	getRiskLevel func(sessionID string) BehaviorRisk,
	getInjection func(sessionID string) int,
) {
	acb.mu.Lock()
	defer acb.mu.Unlock()
	acb.getMode = getMode
	acb.getRiskLevel = getRiskLevel
	acb.getInjection = getInjection
}

// GetThreshold returns the current effective threshold for a session.
func (acb *AdaptiveCircuitBreaker) GetThreshold(sessionID string) int {
	acb.mu.Lock()
	defer acb.mu.Unlock()
	return acb.computeThresholdLocked(sessionID)
}

// RecordDenial records a denial event for adaptive threshold calculation.
func (acb *AdaptiveCircuitBreaker) RecordDenial(sessionID, tool, reason string) {
	acb.mu.Lock()
	defer acb.mu.Unlock()

	rec := denialRecord{
		Time:   time.Now(),
		Reason: reason,
		Tool:   tool,
	}

	history := acb.denialHistory[sessionID]
	history = append(history, rec)

	// Prune expired records
	cutoff := time.Now().Add(-acb.decayDuration)
	valid := make([]denialRecord, 0, len(history))
	for _, r := range history {
		if r.Time.After(cutoff) {
			valid = append(valid, r)
		}
	}

	if len(valid) == 0 {
		delete(acb.denialHistory, sessionID)
	} else {
		acb.denialHistory[sessionID] = valid
	}

	// Update threshold immediately after denial
	acb.sessionThresholds[sessionID] = acb.computeThresholdLocked(sessionID)
}

// ShouldBlock determines whether a session should be blocked due to
// accumulated denials exceeding the adaptive threshold.
func (acb *AdaptiveCircuitBreaker) ShouldBlock(sessionID string) (bool, int, int) {
	acb.mu.Lock()
	defer acb.mu.Unlock()

	threshold := acb.computeThresholdLocked(sessionID)
	history := acb.denialHistory[sessionID]
	count := len(history)
	return count >= threshold, count, threshold
}

// computeThresholdLocked calculates the adaptive threshold for a session.
// Must be called with mu.Lock held.
func (acb *AdaptiveCircuitBreaker) computeThresholdLocked(sessionID string) int {
	// Start with normal threshold
	level := RiskNormal
	threshold := acb.baseThresholds[level]

	// Factor 1: Sandbox mode
	if acb.getMode != nil {
		mode := acb.getMode()
		switch mode {
		case ModeStrict:
			level = maxRiskLevel(level, RiskHigh)
		case ModeReadonly:
			level = maxRiskLevel(level, RiskElevated)
		}
	}

	// Factor 2: Behavior analysis anomalies
	if acb.getRiskLevel != nil {
		risk := acb.getRiskLevel(sessionID)
		switch risk {
		case BehaviorHigh, BehaviorCritical:
			level = maxRiskLevel(level, RiskCritical)
		case BehaviorMedium:
			level = maxRiskLevel(level, RiskHigh)
		case BehaviorLow:
			level = maxRiskLevel(level, RiskElevated)
		}
	}

	// Factor 3: Injection detection count
	if acb.getInjection != nil {
		detections := acb.getInjection(sessionID)
		if detections >= 3 {
			level = maxRiskLevel(level, RiskCritical)
		} else if detections >= 2 {
			level = maxRiskLevel(level, RiskHigh)
		} else if detections >= 1 {
			level = maxRiskLevel(level, RiskElevated)
		}
	}

	// Factor 4: Recent denial rate (3+ denials within 1 minute = suspicious)
	history := acb.denialHistory[sessionID]
	cutoff := time.Now().Add(-1 * time.Minute)
	recentDenials := 0
	for _, r := range history {
		if r.Time.After(cutoff) {
			recentDenials++
		}
	}
	if recentDenials >= 3 {
		level = maxRiskLevel(level, RiskCritical)
	} else if recentDenials >= 2 {
		level = maxRiskLevel(level, RiskHigh)
	}

	// Apply level-based threshold
	baseThreshold, exists := acb.baseThresholds[level]
	if exists {
		threshold = baseThreshold
	}

	// Apply range bounds
	if threshold < acb.minThreshold {
		threshold = acb.minThreshold
	}
	if threshold > acb.maxThreshold {
		threshold = acb.maxThreshold
	}

	return threshold
}

func maxRiskLevel(a, b RiskLevel) RiskLevel {
	if a >= b {
		return a
	}
	return b
}

// GetSessionStats returns circuit breaker statistics for a session.
func (acb *AdaptiveCircuitBreaker) GetSessionStats(sessionID string) CircuitBreakerStats {
	acb.mu.RLock()
	defer acb.mu.RUnlock()

	threshold := acb.computeThresholdLocked(sessionID)
	history := acb.denialHistory[sessionID]
	denialsLast5Min := 0
	cutoff := time.Now().Add(-5 * time.Minute)
	for _, r := range history {
		if r.Time.After(cutoff) {
			denialsLast5Min++
		}
	}

	return CircuitBreakerStats{
		SessionID:      sessionID,
		Threshold:      threshold,
		CurrentCount:   len(history),
		DenialsLast5Min: denialsLast5Min,
		Blocked:        len(history) >= threshold,
		LastDenialAt:   lastDenialTime(history),
		ActiveRisks:    acb.computeRiskBreakdownLocked(sessionID),
	}
}

func (acb *AdaptiveCircuitBreaker) computeRiskBreakdownLocked(sessionID string) []string {
	var risks []string

	if acb.getMode != nil {
		mode := acb.getMode()
		if mode == ModeStrict || mode == ModeReadonly {
			risks = append(risks, "sandbox_mode="+mode.String())
		}
	}

	if acb.getRiskLevel != nil {
		risk := acb.getRiskLevel(sessionID)
		if risk >= BehaviorMedium {
			risks = append(risks, "behavior_risk="+risk.String())
		}
	}

	if acb.getInjection != nil {
		detections := acb.getInjection(sessionID)
		if detections > 0 {
			risks = append(risks, "injection_detections="+intToStr(detections))
		}
	}

	return risks
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	result := ""
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		result = string(rune('0'+i%10)) + result
		i /= 10
	}
	if neg {
		result = "-" + result
	}
	return result
}

func lastDenialTime(history []denialRecord) time.Time {
	var last time.Time
	for _, r := range history {
		if r.Time.After(last) {
			last = r.Time
		}
	}
	return last
}

// CircuitBreakerStats provides a snapshot of the circuit breaker state.
type CircuitBreakerStats struct {
	SessionID       string   `json:"sessionId"`
	Threshold       int      `json:"threshold"`
	CurrentCount    int      `json:"currentCount"`
	DenialsLast5Min  int      `json:"denialsLast5Min"`
	Blocked         bool     `json:"blocked"`
	LastDenialAt    time.Time `json:"lastDenialAt,omitempty"`
	ActiveRisks     []string `json:"activeRisks,omitempty"`
}

// CleanupExpiredSessions removes sessions with no denials for the decay duration.
func (acb *AdaptiveCircuitBreaker) CleanupExpiredSessions() int {
	acb.mu.Lock()
	defer acb.mu.Unlock()
	cutoff := time.Now().Add(-acb.decayDuration)
	removed := 0
	for id, history := range acb.denialHistory {
		valid := make([]denialRecord, 0, len(history))
		for _, r := range history {
			if r.Time.After(cutoff) {
				valid = append(valid, r)
			}
		}
		if len(valid) == 0 {
			delete(acb.denialHistory, id)
			delete(acb.sessionThresholds, id)
			removed++
		} else {
			acb.denialHistory[id] = valid
		}
	}
	return removed
}

// ResetSession completely clears circuit breaker state for a session.
func (acb *AdaptiveCircuitBreaker) ResetSession(sessionID string) {
	acb.mu.Lock()
	defer acb.mu.Unlock()
	delete(acb.denialHistory, sessionID)
	delete(acb.sessionThresholds, sessionID)
}
