package auth

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/tenant"
)

// Principal is the authenticated caller extracted from a valid JWT.
type Principal struct {
	UserID   string
	OrgID    string
	DeviceID string
	Role     string
	Email    string
}

type principalKey struct{}

// WithPrincipal stores the authenticated principal on the context. As a
// convenience (Sprint 1.6) it also stores a derived tenant.Tenant so downstream
// business code can read scoping identifiers via tenant.From without importing
// this package.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	if p == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, principalKey{}, p)
	ctx = tenant.With(ctx, tenant.Tenant{UserID: p.UserID, OrgID: p.OrgID})
	return ctx
}

// PrincipalFrom returns the principal from the context, or nil if absent.
func PrincipalFrom(ctx context.Context) *Principal {
	if p, ok := ctx.Value(principalKey{}).(*Principal); ok {
		return p
	}
	return nil
}
