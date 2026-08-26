package observability

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	authdomain "github.com/spray272598/code-agent/internal/domain/auth"
)

// OTel attribute keys that identify the principal (Sprint 1.8). Keep the names
// stable — dashboards and alerting rules reference them.
const (
	TenantAttrUserID   = attribute.Key("code_agent.user_id")
	TenantAttrDeviceID = attribute.Key("code_agent.device_id")
)

// TenantAttrs returns the OTel attributes that identify the principal. A nil
// principal yields no attributes; empty fields are skipped.
func TenantAttrs(p *authdomain.Principal) []attribute.KeyValue {
	if p == nil {
		return nil
	}
	out := make([]attribute.KeyValue, 0, 2)
	if p.UserID != "" {
		out = append(out, TenantAttrUserID.String(p.UserID))
	}
	if p.DeviceID != "" {
		out = append(out, TenantAttrDeviceID.String(p.DeviceID))
	}
	return out
}

// SetTenantAttrs attaches the principal's identifiers to the given span. Safe
// to call with a nil principal or nil span.
func SetTenantAttrs(span trace.Span, p *authdomain.Principal) {
	if span == nil || p == nil {
		return
	}
	span.SetAttributes(TenantAttrs(p)...)
}

// RequestSpanMiddleware opens an OTel span for each HTTP request. It also tags
// the span with the HTTP method and path so traces are correlatable even before
// the auth middleware enriches them with tenant attributes. Place this BEFORE
// auth so downstream code (e.g. authJWT) can attach tenant attrs via
// SetTenantAttrs.
func RequestSpanMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := StartSpan(r.Context(), "http.request",
			attribute.String("http.method", r.Method),
			attribute.String("http.target", r.URL.Path),
		)
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
