package model

import (
	"math"
	"testing"
)

func TestEstimateUSD(t *testing.T) {
	r := ModelRoute{CostPer1kIn: 0.001, CostPer1kOut: 0.002}
	got := EstimateUSD(1000, 2000, r)
	want := 0.001 + 0.004 // 1*0.001 + 2*0.002
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("EstimateUSD = %v, want %v", got, want)
	}
}

func TestCostTracker_AddAndSpent(t *testing.T) {
	c := NewCostTracker(1.0)
	if c.Add(0.3) != 0.3 {
		t.Fatal("first add should return 0.3")
	}
	if c.Spent() != 0.3 {
		t.Fatalf("spent = %v", c.Spent())
	}
	c.Add(0.2)
	if c.Spent() != 0.5 {
		t.Fatalf("spent = %v", c.Spent())
	}
}

func TestCostTracker_OverBudget(t *testing.T) {
	c := NewCostTracker(0.5)
	c.Add(0.5)
	if !c.OverBudget() {
		t.Fatal("expected over budget at exactly 0.5")
	}
	c.Add(0.1)
	if !c.OverBudget() {
		t.Fatal("expected still over budget")
	}
}

func TestCostTracker_Unlimited(t *testing.T) {
	c := NewCostTracker(0)
	c.Add(999)
	if c.OverBudget() {
		t.Fatal("unlimited budget should never be over")
	}
	if c.Remaining() != -1 {
		t.Fatalf("unlimited remaining should be -1, got %v", c.Remaining())
	}
}

func TestCostTracker_Remaining(t *testing.T) {
	c := NewCostTracker(1.0)
	c.Add(0.25)
	if got := c.Remaining(); math.Abs(got-0.75) > 1e-9 {
		t.Fatalf("remaining = %v, want 0.75", got)
	}
}
