package vector

import (
	"context"
	"errors"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/vector"
)

func TestMemIndexBasicUpsertSearch(t *testing.T) {
	ctx := context.Background()
	idx := NewMemIndex()

	// Three orthogonal-ish 3-d vectors.
	if err := idx.Upsert(ctx, "mem", []vector.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"user_id": "u1"}},
		{ID: "b", Vector: []float32{0, 1, 0}, Payload: map[string]any{"user_id": "u1"}},
		{ID: "c", Vector: []float32{0, 0, 1}, Payload: map[string]any{"user_id": "u2"}},
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := idx.Search(ctx, "mem", []float32{1, 0, 0}, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("top2: want 2 hits, got %d", len(hits))
	}
	if hits[0].ID != "a" {
		t.Fatalf("top1 should be exact-match 'a', got %q", hits[0].ID)
	}

	// payload filter narrows to user u1 only.
	hits, err = idx.Search(ctx, "mem", []float32{1, 0, 0}, 10, map[string]any{"user_id": "u1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Payload["user_id"] != "u1" {
			t.Fatalf("filter leaked: %+v", h)
		}
	}
}

func TestMemIndexDeleteAndUpdate(t *testing.T) {
	ctx := context.Background()
	idx := NewMemIndex()
	if err := idx.Upsert(ctx, "c", []vector.Point{{ID: "x", Vector: []float32{1, 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Delete(ctx, "c", "x"); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(ctx, "c", []float32{1, 1}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("delete failed: still %d hits", len(hits))
	}
	// upsert again after delete works.
	if err := idx.Upsert(ctx, "c", []vector.Point{{ID: "x", Vector: []float32{1, 1}}}); err != nil {
		t.Fatal(err)
	}
}

func TestNoopIndexReturnsUnavailable(t *testing.T) {
	var n NoopIndex
	var _ vector.IVectorIndex = n
	if err := n.Ensure(context.Background(), "c", 4); !errors.Is(err, vector.ErrUnavailable) {
		t.Fatalf("Ensure err = %v, want ErrUnavailable", err)
	}
	if _, err := n.Search(context.Background(), "c", nil, 1, nil); !errors.Is(err, vector.ErrUnavailable) {
		t.Fatalf("Search err = %v", err)
	}
	if err := n.Upsert(context.Background(), "c", nil); !errors.Is(err, vector.ErrUnavailable) {
		t.Fatalf("Upsert err = %v", err)
	}
	if err := n.Delete(context.Background(), "c", "x"); !errors.Is(err, vector.ErrUnavailable) {
		t.Fatalf("Delete err = %v", err)
	}
}