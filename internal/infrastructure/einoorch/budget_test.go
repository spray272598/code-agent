package einoorch

import (
	"testing"
)

func TestBudgetManagerBasic(t *testing.T) {
	bm := NewBudgetManager(1000, 3)

	if bm.MaxAgents() != 3 {
		t.Errorf("MaxAgents() = %d, want 3", bm.MaxAgents())
	}
	if bm.MaxTokens() != 1000 {
		t.Errorf("MaxTokens() = %d, want 1000", bm.MaxTokens())
	}

	if !bm.TryReserveAgents(2) {
		t.Error("TryReserveAgents(2) should succeed")
	}
	if bm.RemainingAgents() != 1 {
		t.Errorf("RemainingAgents() = %d, want 1", bm.RemainingAgents())
	}
	if bm.TryReserveAgents(2) {
		t.Error("TryReserveAgents(2) should fail (only 1 remaining)")
	}
	bm.ReleaseAgents(2)
	if bm.RemainingAgents() != 3 {
		t.Errorf("after release, RemainingAgents() = %d, want 3", bm.RemainingAgents())
	}

	if !bm.ConsumeTokens(400) {
		t.Error("ConsumeTokens(400) should succeed")
	}
	if bm.TokensUsed() != 400 {
		t.Errorf("TokensUsed() = %d, want 400", bm.TokensUsed())
	}
	if bm.ConsumeTokens(700) {
		t.Error("ConsumeTokens(700) should fail (1100 total > 1000)")
	}
	if !bm.ConsumeTokens(600) {
		t.Error("ConsumeTokens(600) should succeed (400+600 = 1000)")
	}
	if bm.RemainingTokens() != 0 {
		t.Errorf("RemainingTokens() = %d, want 0", bm.RemainingTokens())
	}

	epoch := bm.NextEpoch()
	if bm.Epoch() != epoch {
		t.Errorf("Epoch() = %d, want %d", bm.Epoch(), epoch)
	}
}

func TestBudgetManagerNilSafe(t *testing.T) {
	var bm *BudgetManager
	if bm.RemainingAgents() != 0 {
		t.Error("nil RemainingAgents should be 0")
	}
	if !bm.TryReserveAgents(5) {
		t.Error("nil TryReserveAgents should be a safe no-op (return true)")
	}
	if !bm.ConsumeTokens(100) {
		t.Error("nil ConsumeTokens should be a safe no-op (return true)")
	}
	if bm.MaxTokens() != 0 || bm.MaxAgents() != 0 {
		t.Error("nil Max values should be 0")
	}
	if bm.Epoch() != 0 {
		t.Error("nil Epoch should be 0")
	}
}

func TestBudgetManagerConcurrent(t *testing.T) {
	bm := NewBudgetManager(10000, 100)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			bm.TryReserveAgents(1)
			bm.ReleaseAgents(1)
			bm.ConsumeTokens(10)
		}
		close(done)
	}()
	<-done
	if bm.MaxAgents() != 100 {
		t.Errorf("MaxAgents changed: %d", bm.MaxAgents())
	}
	if bm.MaxTokens() != 10000 {
		t.Errorf("MaxTokens changed: %d", bm.MaxTokens())
	}
}
