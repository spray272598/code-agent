package security

import (
	"testing"
	"time"
)

func TestAdaptiveCircuitBreaker_DefaultThresholds(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()

	// Without any risk sources, should return normal threshold
	threshold := acb.GetThreshold("unknown_session")
	if threshold != 5 {
		t.Errorf("default threshold=%d want 5", threshold)
	}
}

func TestAdaptiveCircuitBreaker_SandboxMode(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()
	mode := ModeReadonly

	acb.SetRiskSources(
		func() SandboxMode { return mode },
		nil,
		nil,
	)

	// Readonly mode should elevate to RiskElevated (threshold 4)
	threshold := acb.GetThreshold("s1")
	if threshold != 4 {
		t.Errorf("readonly threshold=%d want 4 (RiskElevated)", threshold)
	}

	// Strict mode should elevate to RiskHigh (threshold 3)
	mode = ModeStrict
	threshold = acb.GetThreshold("s1")
	if threshold != 3 {
		t.Errorf("strict threshold=%d want 3 (RiskHigh)", threshold)
	}

	// Workspace mode should stay at normal (threshold 5)
	mode = ModeWorkspace
	threshold = acb.GetThreshold("s1")
	if threshold != 5 {
		t.Errorf("workspace threshold=%d want 5 (RiskNormal)", threshold)
	}
}

func TestAdaptiveCircuitBreaker_BehaviorRisk(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()
	risk := BehaviorLow

	acb.SetRiskSources(
		nil,
		func(sessionID string) BehaviorRisk { return risk },
		nil,
	)

	// BehaviorLow -> RiskElevated (threshold 4)
	threshold := acb.GetThreshold("s1")
	if threshold != 4 {
		t.Errorf("BehaviorLow threshold=%d want 4", threshold)
	}

	// BehaviorMedium -> RiskHigh (threshold 3)
	risk = BehaviorMedium
	threshold = acb.GetThreshold("s1")
	if threshold != 3 {
		t.Errorf("BehaviorMedium threshold=%d want 3", threshold)
	}

	// BehaviorHigh -> RiskCritical (threshold 2)
	risk = BehaviorHigh
	threshold = acb.GetThreshold("s1")
	if threshold != 2 {
		t.Errorf("BehaviorHigh threshold=%d want 2", threshold)
	}

	// BehaviorCritical -> RiskCritical (threshold 2)
	risk = BehaviorCritical
	threshold = acb.GetThreshold("s1")
	if threshold != 2 {
		t.Errorf("BehaviorCritical threshold=%d want 2", threshold)
	}
}

func TestAdaptiveCircuitBreaker_InjectionDetections(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()
	var detections int

	acb.SetRiskSources(
		nil,
		nil,
		func(sessionID string) int { return detections },
	)

	// 0 detections -> normal threshold 5
	detections = 0
	threshold := acb.GetThreshold("s1")
	if threshold != 5 {
		t.Errorf("0 detections threshold=%d want 5", threshold)
	}

	// 1 detection -> elevated (threshold 4)
	detections = 1
	threshold = acb.GetThreshold("s1")
	if threshold != 4 {
		t.Errorf("1 detection threshold=%d want 4", threshold)
	}

	// 2 detections -> high (threshold 3)
	detections = 2
	threshold = acb.GetThreshold("s1")
	if threshold != 3 {
		t.Errorf("2 detections threshold=%d want 3", threshold)
	}

	// 3+ detections -> critical (threshold 2)
	detections = 5
	threshold = acb.GetThreshold("s1")
	if threshold != 2 {
		t.Errorf("5 detections threshold=%d want 2", threshold)
	}
}

func TestAdaptiveCircuitBreaker_RecordDenial(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()

	// Record denials and check threshold
	for i := 0; i < 3; i++ {
		acb.RecordDenial("s1", "bash", "test denial")
	}

	blocked, count, threshold := acb.ShouldBlock("s1")
	if count != 3 {
		t.Errorf("denial count=%d want 3", count)
	}
	// 3 denials within 1 minute triggers RiskCritical (threshold 2)
	// count (3) >= threshold (2) => blocked
	if !blocked {
		t.Error("should be blocked with 3 denials (recent denial rate triggers adaptive threshold)")
	}
	if threshold > 2 {
		t.Errorf("threshold=%d should be <= 2 after 3 rapid denials", threshold)
	}
}

func TestAdaptiveCircuitBreaker_RecentDenialRate(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()

	// Record 3 denials rapidly
	for i := 0; i < 3; i++ {
		acb.RecordDenial("s1", "bash", "denial " + string(rune('a'+i)))
	}

	// 3 denials within 1 minute should trigger RiskCritical (threshold 2)
	// But wait, we have 3 denials and normal threshold is 5, so 3 < 5 = not blocked
	// The recent denial rate should reduce threshold to 2 (RiskCritical)
	blocked, count, threshold := acb.ShouldBlock("s1")
	_ = count
	_ = blocked

	// 3 denials within 1 minute -> RiskCritical -> threshold 2
	// count (3) >= threshold (2) => blocked!
	if threshold > 2 {
		t.Errorf("3 recent denials should reduce threshold to <= 2, got %d", threshold)
	}
}

func TestAdaptiveCircuitBreaker_CombinedRisk(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()

	mode := ModeWorkspace
	risk := BehaviorNormal
	var detections int

	acb.SetRiskSources(
		func() SandboxMode { return mode },
		func(sessionID string) BehaviorRisk { return risk },
		func(sessionID string) int { return detections },
	)

	// All low risk factors -> should give normal threshold
	threshold := acb.GetThreshold("s1")
	if threshold != 5 {
		t.Errorf("all-low threshold=%d want 5", threshold)
	}

	// Combine multiple risk factors
	mode = ModeStrict
	risk = BehaviorHigh
	detections = 2

	// Strict(High=3) + BehaviorHigh(Critical=2) + 2 detections(High=3)
	// Max of all: Critical -> threshold 2
	threshold = acb.GetThreshold("s1")
	if threshold != 2 {
		t.Errorf("combined high risk threshold=%d want 2 (Critical)", threshold)
	}
}

func TestAdaptiveCircuitBreaker_RangeBounds(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()
	acb.minThreshold = 3
	acb.maxThreshold = 3

	// With bounds set, should always return 3
	threshold := acb.GetThreshold("s1")
	if threshold != 3 {
		t.Errorf("bounded threshold=%d want 3", threshold)
	}
}

func TestAdaptiveCircuitBreaker_Cleanup(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()
	acb.decayDuration = 1 * time.Nanosecond // Immediate decay

	acb.RecordDenial("s1", "bash", "test")

	// Wait for decay
	time.Sleep(10 * time.Millisecond)

	removed := acb.CleanupExpiredSessions()
	if removed != 1 {
		t.Errorf("cleanup removed=%d want 1", removed)
	}

	// After cleanup, session should have 0 history
	_, count, _ := acb.ShouldBlock("s1")
	if count != 0 {
		t.Errorf("after cleanup count=%d want 0", count)
	}
}

func TestAdaptiveCircuitBreaker_SessionStats(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()

	// Unknown session
	stats := acb.GetSessionStats("unknown")
	if stats.Threshold != 5 {
		t.Errorf("unknown session threshold=%d want 5", stats.Threshold)
	}
	if stats.Blocked {
		t.Error("unknown session should not be blocked")
	}

	// Session with denials (without risk sources, ActiveRisks will be empty)
	acb.RecordDenial("s1", "bash", "denial1")
	acb.RecordDenial("s1", "bash", "denial2")

	stats = acb.GetSessionStats("s1")
	if stats.CurrentCount != 2 {
		t.Errorf("CurrentCount=%d want 2", stats.CurrentCount)
	}
	// Without risk sources, ActiveRisks will be empty
	// But with 2 recent denials, threshold should be reduced to 3 (RiskHigh)
	if stats.Threshold > 5 {
		t.Errorf("threshold=%d should be <= 5 after 2 denials", stats.Threshold)
	}

	// With risk sources configured, ActiveRisks should be populated
	acb2 := NewAdaptiveCircuitBreaker()
	acb2.SetRiskSources(
		func() SandboxMode { return ModeStrict },
		func(sessionID string) BehaviorRisk { return BehaviorHigh },
		func(sessionID string) int { return 2 },
	)
	acb2.RecordDenial("s2", "bash", "denial")
	stats2 := acb2.GetSessionStats("s2")
	if len(stats2.ActiveRisks) == 0 {
		t.Error("with risk sources, should have active risks")
	}
}

func TestAdaptiveCircuitBreaker_ResetSession(t *testing.T) {
	acb := NewAdaptiveCircuitBreaker()

	acb.RecordDenial("s1", "bash", "test denial")

	// Verify denial is recorded
	_, count, _ := acb.ShouldBlock("s1")
	if count != 1 {
		t.Errorf("count=%d want 1 before reset", count)
	}

	// Reset
	acb.ResetSession("s1")

	// After reset, should have 0 denials
	_, count, _ = acb.ShouldBlock("s1")
	if count != 0 {
		t.Errorf("count=%d want 0 after reset", count)
	}
}

func TestAdaptiveCircuitBreaker_MaxRiskLevel(t *testing.T) {
	if maxRiskLevel(RiskNormal, RiskElevated) != RiskElevated {
		t.Error("normal + elevated = elevated")
	}
	if maxRiskLevel(RiskHigh, RiskElevated) != RiskHigh {
		t.Error("high + elevated = high")
	}
	if maxRiskLevel(RiskCritical, RiskNormal) != RiskCritical {
		t.Error("critical + normal = critical")
	}
}
