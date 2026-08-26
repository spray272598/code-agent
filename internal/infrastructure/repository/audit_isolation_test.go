package repository

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/audit"
)

// TestAuditMultiTenantIsolation (Sprint 1.7) guarantees that audit reads are
// strictly scoped to the authenticated user. User A must not see user B's
// entries even when both share a session id.
func TestAuditMultiTenantIsolation(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryAuditRepo()

	// two users share a session id on purpose
	for _, e := range []audit.Entry{
		{UserID: "alice", SessionID: "sess-shared", Action: "tool_call", Tool: "read", Detail: "alice-1"},
		{UserID: "bob", SessionID: "sess-shared", Action: "tool_call", Tool: "write", Detail: "bob-1"},
		{UserID: "alice", SessionID: "sess-shared", Action: "permission", Detail: "alice-2"},
		{UserID: "bob", SessionID: "sess-other", Action: "tool_call", Tool: "edit", Detail: "bob-2"},
	} {
		if err := r.Append(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// alice querying the shared session only sees alice's two entries.
	list, err := r.ListBySession(ctx, "alice", "sess-shared", 100)
	if err != nil {
		t.Fatalf("alice list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("alice sess-shared: want 2, got %d", len(list))
	}
	for _, e := range list {
		if e.UserID != "alice" {
			t.Fatalf("cross-tenant leak: got user=%q detail=%q", e.UserID, e.Detail)
		}
	}

	// bob querying the same shared session only sees bob's one entry.
	list, err = r.ListBySession(ctx, "bob", "sess-shared", 100)
	if err != nil {
		t.Fatalf("bob list: %v", err)
	}
	if len(list) != 1 || list[0].UserID != "bob" || list[0].Detail != "bob-1" {
		t.Fatalf("bob sess-shared: want 1 bob-1, got %+v", list)
	}

	// bob querying all his sessions sees both bob entries (no session filter).
	list, err = r.ListBySession(ctx, "bob", "", 100)
	if err != nil {
		t.Fatalf("bob all: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("bob all: want 2, got %d", len(list))
	}

	// an unknown user gets nothing regardless of the session id.
	list, err = r.ListBySession(ctx, "eve", "sess-shared", 100)
	if err != nil {
		t.Fatalf("eve list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("eve sess-shared: want 0 (cross-tenant leak), got %d", len(list))
	}

	// alice querying bob's private session sees nothing.
	list, err = r.ListBySession(ctx, "alice", "sess-other", 100)
	if err != nil {
		t.Fatalf("alice sess-other: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("alice sess-other: want 0 (cross-tenant leak), got %d", len(list))
	}
}
