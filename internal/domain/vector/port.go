// Package vector is the Sprint 1.9 abstraction for the dense-vector index used
// by memory retrieval (Search / FindDuplicate / BackfillVector).
//
// The interface is intentionally minimal: enough for the in-process cosine
// rerank today and for a future remote backend (Qdrant / Milvus / pgvector)
// without changing call sites. Backends are constructed in infrastructure/vector.
package vector

import (
	"context"
	"errors"
)

// ErrUnavailable is returned when the index backend is not configured. The
// memory service treats this as "skip the vector path" — keyword search keeps
// working (Sprint 1.10 fallback).
var ErrUnavailable = errors.New("vector index unavailable")

// Point is a single vector record with a stable id and opaque payload (the
// caller can stash user/project metadata for filtering).
type Point struct {
	ID      string         // stable id (memory.ID as string is fine)
	Vector  []float32      // dense embedding
	Payload map[string]any // optional: {"user_id":"...","project_id":"..."}
}

// Hit is a search result: scored point in descending similarity order.
type Hit struct {
	ID      string
	Score   float32
	Payload map[string]any
}

// EnsureCollection is a hint that the backend should prepare the named
// collection with the given vector dimension. Backends that don't need
// pre-creation may return nil (e.g. the in-process default). Must be idempotent.
type EnsureCollection func(ctx context.Context, name string, dim int) error

// IVectorIndex is the abstraction. Implementations MUST be safe for concurrent
// use; methods return ctx.Err() on cancellation.
type IVectorIndex interface {
	// Ensure prepares the collection (no-op if already prepared).
	Ensure(ctx context.Context, collection string, dim int) error
	// Upsert inserts or updates points in the collection.
	Upsert(ctx context.Context, collection string, points []Point) error
	// Search returns the top-k most similar points to query, scored in
	// descending order. An optional filter narrows the candidate set (e.g.
	// payload["user_id"] == "<authenticated user>" for multi-tenant safety).
	Search(ctx context.Context, collection string, query []float32, topK int, filter map[string]any) ([]Hit, error)
	// Delete removes a point by id. Missing ids are not an error.
	Delete(ctx context.Context, collection string, id string) error
}

// MatchPayload returns true when the hit's payload contains all key/value pairs
// in filter. Implementations may use this or push the filter down to the
// backend.
func MatchPayload(payload map[string]any, filter map[string]any) bool {
	for k, want := range filter {
		got, ok := payload[k]
		if !ok || got != want {
			return false
		}
	}
	return true
}
