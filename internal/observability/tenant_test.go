package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	authdomain "github.com/spray272598/code-agent/internal/domain/auth"
)

// TestTenantAttrs (Sprint 1.8) verifies that Principal fields map to the
// expected OTel attribute keys and that nil/empty values are skipped safely.
func TestTenantAttrs(t *testing.T) {
	t.Run("nil principal yields no attrs", func(t *testing.T) {
		if got := TenantAttrs(nil); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("empty principal yields no attrs", func(t *testing.T) {
		if got := TenantAttrs(&authdomain.Principal{}); len(got) != 0 {
			t.Fatalf("expected 0 attrs, got %d", len(got))
		}
	})

	t.Run("full principal maps to stable keys", func(t *testing.T) {
		attrs := TenantAttrs(&authdomain.Principal{
			UserID:   "usr_01",
			DeviceID: "dev_01",
		})
		want := map[attribute.Key]string{
			TenantAttrUserID:   "usr_01",
			TenantAttrDeviceID: "dev_01",
		}
		if len(attrs) != len(want) {
			t.Fatalf("want %d attrs, got %d (%v)", len(want), len(attrs), attrs)
		}
		for _, kv := range attrs {
			v, ok := want[kv.Key]
			if !ok {
				t.Fatalf("unexpected key %q", kv.Key)
			}
			if kv.Value.AsString() != v {
				t.Fatalf("key %q: want %q, got %q", kv.Key, v, kv.Value.AsString())
			}
		}
	})
}

// TestSetTenantAttrsOnSpan exercises the full path: start a span, attach the
// principal, and confirm SetTenantAttrs tolerates a nil span without panicking.
func TestSetTenantAttrsOnSpan(t *testing.T) {
	_, span := StartSpan(context.Background(), "test")
	defer span.End()
	SetTenantAttrs(span, &authdomain.Principal{UserID: "usr_01"})

	// nil span must be a no-op (defensive against misconfigured middleware).
	SetTenantAttrs(nil, &authdomain.Principal{UserID: "usr_01"})

	// nil principal must be a no-op.
	SetTenantAttrs(span, nil)
}
