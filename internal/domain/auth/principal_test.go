package auth

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/tenant"
)

// TestWithPrincipalStampsTenant verifies the Sprint 1.6 bridge: WithPrincipal
// must also stash a tenant.Tenant so downstream code can read ctx.tenant.
// UserID without importing this package.
func TestWithPrincipalStampsTenant(t *testing.T) {
	p := &Principal{UserID: "usr_01", DeviceID: "dev_01"}
	ctx := WithPrincipal(context.Background(), p)

	if got := PrincipalFrom(ctx); got != p {
		t.Fatalf("PrincipalFrom mismatch")
	}
	if got := tenant.UserID(ctx); got != "usr_01" {
		t.Fatalf("tenant.UserID = %q", got)
	}
}

func TestWithPrincipalNilIsSafe(t *testing.T) {
	ctx := WithPrincipal(context.Background(), nil)
	if PrincipalFrom(ctx) != nil {
		t.Fatalf("nil principal should not be stashed")
	}
	if _, ok := tenant.From(ctx); ok {
		t.Fatalf("nil principal must not produce a tenant")
	}
	// also: must not panic and must keep the original ctx usable.
	_ = ctx
}