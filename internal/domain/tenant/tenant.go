// Package tenant is the Sprint 1.6 multi-tenant context primitive.
//
// Tenant is the canonical, lightweight shape carried in ctx for every business
// layer call: it identifies the authenticated user so that downstream
// repositories, services and middleware can derive the row-level filter
// without taking userID as an explicit parameter at every call site.
//
// Tenant is intentionally a subset of authdomain.Principal (no JWT fields like
// Role/Email/DeviceID). Use Principal when you need identity metadata; use
// Tenant when you only need scoping.
package tenant

import "context"

// Tenant is the scope for a request.
type Tenant struct {
	UserID string
}

func (t Tenant) IsZero() bool { return t.UserID == "" }

type ctxKey struct{}

// With stores the Tenant on ctx and returns the new context.
func With(ctx context.Context, t Tenant) context.Context {
	if t.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, t)
}

// From returns the Tenant from ctx and a bool indicating presence. Callers
// MUST handle the false case defensively (return 401, log + skip, etc.) —
// Tenant may legitimately be absent for system/maintenance tasks.
func From(ctx context.Context) (Tenant, bool) {
	if ctx == nil {
		return Tenant{}, false
	}
	t, ok := ctx.Value(ctxKey{}).(Tenant)
	return t, ok
}

// MustFrom returns the Tenant from ctx, or the zero value if absent. Use only
// when you have a strong invariant that the caller is authenticated (e.g. in
// handlers downstream of authJWT).
func MustFrom(ctx context.Context) Tenant {
	t, _ := From(ctx)
	return t
}

// UserID returns just the user id (empty string when absent).
func UserID(ctx context.Context) string {
	t, _ := From(ctx)
	return t.UserID
}