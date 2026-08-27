package contextx

import (
	"testing"
)

// TestContextIntegratorRecordLLMUsageAnchorsBudget verifies that a real provider
// token count, once recorded, becomes the ground-truth input usage used by the
// budget's compression decision — overriding the EstimateTokens heuristic.
func TestContextIntegratorRecordLLMUsageAnchorsBudget(t *testing.T) {
	// Budget: total 1000 → input 800, output 200.
	bm := NewBudgetManager(1000)
	ci := NewContextIntegrator(NewCompressor(500), bm, nil)

	// Before any LLM call, Evaluate uses the passed heuristic estimate (0 here).
	if got := bm.Evaluate(0).UsedInput; got != 0 {
		t.Fatalf("precondition: expected UsedInput 0, got %d", got)
	}
	// No real usage → low pressure → no compression.
	if bm.ShouldCompress(0, 0.8) {
		t.Fatal("precondition: expected no compression at 0 usage")
	}

	// Simulate a real LLM response reporting 900 prompt tokens (over the 800 input budget).
	ci.RecordLLMUsage(900, 120)

	// Evaluate now anchors UsedInput to the real count, overriding the heuristic.
	if got := bm.Evaluate(0).UsedInput; got != 900 {
		t.Fatalf("expected UsedInput anchored to 900, got %d", got)
	}
	// Pressure ratio is capped at 1.0, so compression should trigger at 0.8 threshold.
	if st := bm.Evaluate(0); st.PressureRatio != 1.0 {
		t.Fatalf("expected pressure ratio capped at 1.0, got %f", st.PressureRatio)
	}
	if !bm.ShouldCompress(0, 0.8) {
		t.Fatal("expected compression to trigger once real usage exceeds threshold")
	}
}

// TestContextIntegratorRecordLLMUsageIgnoresInvalid ensures non-positive readings
// do not clobber a previously anchored valid reading.
func TestContextIntegratorRecordLLMUsageIgnoresInvalid(t *testing.T) {
	bm := NewBudgetManager(1000)
	ci := NewContextIntegrator(NewCompressor(500), bm, nil)

	ci.RecordLLMUsage(500, 0)
	if got := bm.Evaluate(0).UsedInput; got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}
	// Non-positive readings must be ignored so the valid anchor is preserved.
	ci.RecordLLMUsage(0, 0)
	ci.RecordLLMUsage(-10, 0)
	if got := bm.Evaluate(0).UsedInput; got != 500 {
		t.Fatalf("expected anchor preserved at 500, got %d", got)
	}
}

// TestContextIntegratorRecordLLMUsageLatestWins ensures the most recent valid
// reading replaces the previous one.
func TestContextIntegratorRecordLLMUsageLatestWins(t *testing.T) {
	bm := NewBudgetManager(1000)
	ci := NewContextIntegrator(NewCompressor(500), bm, nil)

	ci.RecordLLMUsage(200, 0)
	ci.RecordLLMUsage(700, 0)
	if got := bm.Evaluate(0).UsedInput; got != 700 {
		t.Fatalf("expected latest reading 700 to win, got %d", got)
	}
}

// TestContextIntegratorRecordLLMUsageNilSafe must not panic when the integrator
// or its budget manager is nil.
func TestContextIntegratorRecordLLMUsageNilSafe(t *testing.T) {
	var ci *ContextIntegrator
	ci.RecordLLMUsage(100, 10) // nil receiver → no panic

	ci2 := &ContextIntegrator{} // budgetMgr nil → no panic
	ci2.RecordLLMUsage(100, 10)
}
