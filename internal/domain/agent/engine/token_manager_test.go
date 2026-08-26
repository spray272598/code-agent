package engine

import (
	"sync"
	"testing"
)

func TestTokenManager_ReserveReleaseConcurrency(t *testing.T) {
	tm := NewTokenManager(1000)
	var wg sync.WaitGroup
	const goroutines = 50
	const reserveEach = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				tm.Reserve(reserveEach)
				tm.Release(reserveEach)
			}
		}()
	}
	wg.Wait()

	bs := tm.State()
	if bs.Reserved != 0 {
		t.Fatalf("expected reserved=0 after all releases, got %d", bs.Reserved)
	}
	if bs.Spent != 0 {
		t.Fatalf("expected spent=0, got %d", bs.Spent)
	}
	if bs.Remaining != 1000 {
		t.Fatalf("expected remaining=1000, got %d", bs.Remaining)
	}
}

func TestTokenManager_BudgetExhaustionPrevention(t *testing.T) {
	tm := NewTokenManager(100)

	if !tm.Reserve(60) {
		t.Fatal("reserve 60 should succeed with budget 100")
	}
	if !tm.Reserve(30) {
		t.Fatal("reserve 30 should succeed with budget 100 and 60 reserved")
	}
	if tm.Reserve(20) {
		t.Fatal("reserve 20 should fail: only 10 remaining")
	}
	if !tm.Reserve(10) {
		t.Fatal("reserve 10 should succeed: exactly fills budget")
	}
	if tm.Reserve(1) {
		t.Fatal("reserve 1 should fail: budget fully utilized")
	}

	bs := tm.State()
	if bs.Reserved != 100 {
		t.Fatalf("expected reserved=100, got %d", bs.Reserved)
	}
	if bs.Remaining != 0 {
		t.Fatalf("expected remaining=0, got %d", bs.Remaining)
	}
}

func TestTokenManager_StateConsistency(t *testing.T) {
	tm := NewTokenManager(200)

	tm.Reserve(50)
	tm.Commit(30)

	bs := tm.State()
	if bs.Total != 200 {
		t.Fatalf("Total mismatch: want 200 got %d", bs.Total)
	}
	if bs.Spent != 30 {
		t.Fatalf("Spent mismatch: want 30 got %d", bs.Spent)
	}
	if bs.Reserved != 20 {
		t.Fatalf("Reserved mismatch: want 20 got %d", bs.Reserved)
	}
	if bs.Used != 50 {
		t.Fatalf("Used mismatch: want 50 got %d (spent+reserved)", bs.Used)
	}
	if bs.Remaining != 150 {
		t.Fatalf("Remaining mismatch: want 150 got %d", bs.Remaining)
	}

	bs2 := tm.State()
	if bs != bs2 {
		t.Fatal("State() not idempotent")
	}
}

func TestTokenManager_CommitFlow(t *testing.T) {
	tm := NewTokenManager(100)

	if !tm.Reserve(80) {
		t.Fatal("reserve 80 should succeed")
	}

	tm.Commit(50)

	bs := tm.State()
	if bs.Spent != 50 {
		t.Fatalf("Spent should be 50 after commit, got %d", bs.Spent)
	}
	if bs.Reserved != 30 {
		t.Fatalf("Reserved should be 30 after partial commit, got %d", bs.Reserved)
	}

	tm.Commit(30)

	bs = tm.State()
	if bs.Spent != 80 {
		t.Fatalf("Spent should be 80 after full commit, got %d", bs.Spent)
	}
	if bs.Reserved != 0 {
		t.Fatalf("Reserved should be 0 after full commit, got %d", bs.Reserved)
	}
	if bs.Remaining != 20 {
		t.Fatalf("Remaining should be 20, got %d", bs.Remaining)
	}
}

func TestTokenManager_ReleaseFlow(t *testing.T) {
	tm := NewTokenManager(100)

	tm.Reserve(70)
	tm.Release(30)

	bs := tm.State()
	if bs.Reserved != 40 {
		t.Fatalf("Reserved should be 40 after partial release, got %d", bs.Reserved)
	}

	tm.Release(100)

	bs = tm.State()
	if bs.Reserved != 0 {
		t.Fatalf("Reserved should be 0 after over-release (capped), got %d", bs.Reserved)
	}
	if bs.Remaining != 100 {
		t.Fatalf("Remaining should be 100, got %d", bs.Remaining)
	}
}

func TestTokenManager_NegativeBudget(t *testing.T) {
	tm := NewTokenManager(50)

	if !tm.Reserve(30) {
		t.Fatal("reserve 30 should succeed")
	}
	tm.Commit(30)

	bs := tm.State()
	if bs.Remaining != 20 {
		t.Fatalf("expected remaining=20, got %d", bs.Remaining)
	}

	if tm.Remaining() < 0 {
		t.Fatal("Remaining should never be negative")
	}

	if tm.Reserve(30) {
		t.Fatal("reserve beyond remaining should fail")
	}

	bs = tm.State()
	if bs.Remaining < 0 {
		t.Fatal("State.Remaining should never be negative")
	}
}

func TestTokenManager_DeterministicMode(t *testing.T) {
	tm := NewTokenManager(100)

	if tm.IsDeterministic() {
		t.Fatal("expected non-deterministic by default")
	}

	tm.DeterministicMode = true
	if !tm.IsDeterministic() {
		t.Fatal("expected deterministic after setting flag")
	}

	tm.DeterministicMode = false
	if tm.IsDeterministic() {
		t.Fatal("expected non-deterministic after clearing flag")
	}
}

func TestTokenManager_ZeroAndNegativeReserve(t *testing.T) {
	tm := NewTokenManager(100)

	if !tm.Reserve(0) {
		t.Fatal("reserve 0 should always succeed")
	}
	if !tm.Reserve(-5) {
		t.Fatal("negative reserve should succeed (no-op)")
	}

	bs := tm.State()
	if bs.Reserved != 0 {
		t.Fatalf("reserved should be 0 after zero/negative reserve, got %d", bs.Reserved)
	}
}

func TestTokenManager_ZeroAndNegativeCommit(t *testing.T) {
	tm := NewTokenManager(100)
	tm.Reserve(50)

	tm.Commit(0)
	tm.Commit(-5)

	bs := tm.State()
	if bs.Spent != 0 {
		t.Fatalf("spent should be 0 after zero/negative commit, got %d", bs.Spent)
	}
	if bs.Reserved != 50 {
		t.Fatalf("reserved should still be 50, got %d", bs.Reserved)
	}
}

func TestTokenManager_ZeroAndNegativeRelease(t *testing.T) {
	tm := NewTokenManager(100)
	tm.Reserve(50)

	tm.Release(0)
	tm.Release(-5)

	bs := tm.State()
	if bs.Reserved != 50 {
		t.Fatalf("reserved should still be 50, got %d", bs.Reserved)
	}
}

func TestTokenManager_CommitExceedsReserved(t *testing.T) {
	tm := NewTokenManager(100)
	tm.Reserve(30)
	tm.Commit(50)

	bs := tm.State()
	if bs.Spent != 30 {
		t.Fatalf("commit should cap at reserved: spent=30 got %d", bs.Spent)
	}
	if bs.Reserved != 0 {
		t.Fatalf("commit should drain reserved: reserved=0 got %d", bs.Reserved)
	}
}

func TestTokenManager_NewTokenManagerDefault(t *testing.T) {
	tm := NewTokenManager(0)
	if tm.Budget != DefaultTokenBudget {
		t.Fatalf("expected default budget %d, got %d", DefaultTokenBudget, tm.Budget)
	}

	tm2 := NewTokenManager(-1)
	if tm2.Budget != DefaultTokenBudget {
		t.Fatalf("expected default budget for negative input, got %d", tm2.Budget)
	}
}

func TestTokenManager_RemainingMatchesState(t *testing.T) {
	tm := NewTokenManager(500)
	tm.Reserve(100)
	tm.Commit(40)

	bs := tm.State()
	if tm.Remaining() != bs.Remaining {
		t.Fatalf("Remaining()=%d != State().Remaining=%d", tm.Remaining(), bs.Remaining)
	}
}

func TestTokenManager_ExhaustedWithReserved(t *testing.T) {
	tm := NewTokenManager(100)
	tm.Reserve(60)

	if tm.Exhausted(30) {
		t.Fatal("used(30) + reserved(60) = 90 < 100, should NOT be exhausted")
	}

	if !tm.Exhausted(40) {
		t.Fatal("used(40) + reserved(60) = 100 >= 100, should be exhausted")
	}
}

func TestTokenManager_PressureBackwardCompat(t *testing.T) {
	tm := NewTokenManager(100)
	tm.Reserve(20)

	if tm.Pressure(70, nil, "") {
		t.Fatal("used(70) + reserved(20) = 90 < 100, should NOT show pressure")
	}

	if !tm.Pressure(80, nil, "") {
		t.Fatal("used(80) + reserved(20) = 100 >= 100, should show pressure")
	}
}
