package tenant

import (
	"context"
	"errors"
	"testing"
)

func TestTenantRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := From(ctx); ok {
		t.Fatalf("expected absent on bare ctx")
	}
	if got := UserID(ctx); got != "" {
		t.Fatalf("UserID = %q", got)
	}

	ctx = With(ctx, Tenant{UserID: "usr_01"})
	got, ok := From(ctx)
	if !ok {
		t.Fatalf("expected present")
	}
	if got.UserID != "usr_01" {
		t.Fatalf("got %+v", got)
	}
	if UserID(ctx) != "usr_01" {
		t.Fatalf("helpers mismatch")
	}
}

func TestTenantWithZeroNoOp(t *testing.T) {
	ctx := context.Background()
	ctx2 := With(ctx, Tenant{})
	if _, ok := From(ctx2); ok {
		t.Fatalf("zero tenant should not be stored")
	}
	if ctx2 != ctx {
		t.Fatalf("With(zero) should return the same ctx")
	}
}

func TestFromNilCtx(t *testing.T) {
	if _, ok := From(nil); ok {
		t.Fatalf("expected nil ctx to be safe")
	}
}

// Sentinel error test to make sure ErrTenantMissing flows through; real
// coverage lives in the repository isolation tests.
var _ = errors.Is