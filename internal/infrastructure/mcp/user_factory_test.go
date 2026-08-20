package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/tenant"
)

// TestUserFactory_PerUserIsolation (Sprint 1.6) is the runtime guarantee:
// the factory must hand each user its own Manager, and the Manager must
// refuse to execute a tool on behalf of a different tenant.
func TestUserFactory_PerUserIsolation(t *testing.T) {
	f := NewUserFactory(func(uid string) *Manager { return NewUserManager(uid) })

	// alice's ctx → alice's manager (first request builds)
	aliceCtx := tenant.With(context.Background(), tenant.Tenant{UserID: "alice"})
	a1, err := f.For(aliceCtx)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := f.For(aliceCtx)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatalf("factory should return the same Manager for the same user across requests")
	}

	// bob's ctx → a different Manager
	bobCtx := tenant.With(context.Background(), tenant.Tenant{UserID: "bob"})
	b, err := f.For(bobCtx)
	if err != nil {
		t.Fatal(err)
	}
	if a1 == b {
		t.Fatalf("alice and bob must receive different Manager instances")
	}

	// factory metrics: alice created + reused, bob created
	m := f.Metrics()
	if m.Created < 2 {
		t.Fatalf("expected ≥2 Created (alice + bob), got %d", m.Created)
	}
	if m.Reused < 1 {
		t.Fatalf("expected ≥1 Reused (alice second call), got %d", m.Reused)
	}
}

func TestUserFactory_RequiresTenant(t *testing.T) {
	f := NewUserFactory(func(uid string) *Manager { return NewUserManager(uid) })
	if _, err := f.For(context.Background()); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("missing tenant: want ErrTenantMismatch, got %v", err)
	}
	if _, err := f.ForUserID(""); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("empty userID: want ErrTenantMismatch, got %v", err)
	}
}

func TestUserFactory_NilSafety(t *testing.T) {
	var nilFactory *UserFactory
	if _, err := nilFactory.For(context.Background()); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("nil factory: want ErrTenantMismatch, got %v", err)
	}
	if nilFactory.Count() != 0 {
		t.Fatalf("nil factory count")
	}
	nilFactory.ResetAll() // must not panic
}

func TestUserFactory_PrimeSystem(t *testing.T) {
	f := NewUserFactory(func(uid string) *Manager { return NewUserManager(uid) })
	sys := NewUserManager("")
	f.PrimeSystem(sys)

	got, err := f.ForUserID("")
	if err != nil {
		t.Fatal(err)
	}
	if got != sys {
		t.Fatalf("PrimeSystem should make the system Manager the cached entry for \"\"")
	}
}

// TestManager_AssertTenant checks the runtime guard. A Manager built for
// "alice" must reject tool calls whose ctx carries a different asserted user.
func TestManager_AssertTenant(t *testing.T) {
	m := NewUserManager("alice")
	defer m.Close()

	// alice's ctx → assertion passes (no tool to call, but AssertTenant returns nil)
	aliceCtx := WithAssertedUser(context.Background(), "alice")
	if err := m.AssertTenant(aliceCtx); err != nil {
		t.Fatalf("alice's ctx should pass: %v", err)
	}

	// bob's ctx → assertion fails
	bobCtx := WithAssertedUser(context.Background(), "bob")
	if err := m.AssertTenant(bobCtx); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("bob's ctx must be rejected, got %v", err)
	}

	// un-asserted ctx (no WithAssertedUser) → rejected (no implicit access)
	if err := m.AssertTenant(context.Background()); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("un-asserted ctx must be rejected, got %v", err)
	}

	// system-owned Manager → no assertion
	sys := NewUserManager("")
	defer sys.Close()
	if err := sys.AssertTenant(context.Background()); err != nil {
		t.Fatalf("system Manager must allow un-asserted ctx, got %v", err)
	}
}

func TestManager_Owner(t *testing.T) {
	m := NewUserManager("alice")
	defer m.Close()
	if got := m.Owner(); got != "alice" {
		t.Fatalf("Owner = %q, want alice", got)
	}
}

func TestUserFactory_ResetAll(t *testing.T) {
	f := NewUserFactory(func(uid string) *Manager { return NewUserManager(uid) })
	for _, uid := range []string{"a", "b", "c"} {
		if _, err := f.ForUserID(uid); err != nil {
			t.Fatal(err)
		}
	}
	if f.Count() != 3 {
		t.Fatalf("want 3 cached, got %d", f.Count())
	}
	f.ResetAll()
	if f.Count() != 0 {
		t.Fatalf("after ResetAll want 0, got %d", f.Count())
	}
	// factory still usable: next request rebuilds
	if _, err := f.ForUserID("a"); err != nil {
		t.Fatal(err)
	}
}