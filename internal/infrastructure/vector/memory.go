// Package vector provides concrete IVectorIndex implementations. Today it
// ships an in-process brute-force cosine index (good for single-node dev /
// test) and a NoopIndex (returns ErrUnavailable so callers fall back to the
// keyword path). A future Sprint 1.9-1.12 Qdrant implementation lives in
// internal/infrastructure/vector/qdrant/ once a network registry is available.
package vector

import (
	"context"
	"sort"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/vector"
)

// MemIndex is an in-process brute-force cosine index. It is safe for
// concurrent use. Single-process only; persistence is the caller's job.
type MemIndex struct {
	mu          sync.RWMutex
	collections map[string]map[string]vector.Point
}

// NewMemIndex returns an empty MemIndex.
func NewMemIndex() *MemIndex {
	return &MemIndex{collections: make(map[string]map[string]vector.Point)}
}

// Ensure is a no-op for the in-process backend; collections are created lazily.
func (m *MemIndex) Ensure(_ context.Context, _ string, _ int) error { return nil }

// Upsert stores each point under its id, replacing any existing point with the
// same id in the same collection.
func (m *MemIndex) Upsert(_ context.Context, collection string, points []vector.Point) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	col, ok := m.collections[collection]
	if !ok {
		col = make(map[string]vector.Point, len(points))
		m.collections[collection] = col
	}
	for _, p := range points {
		col[p.ID] = p
	}
	return nil
}

// Search performs a brute-force cosine scan over the collection. filter, when
// non-empty, is applied to the payload before scoring (post-filter).
func (m *MemIndex) Search(_ context.Context, collection string, query []float32, topK int, filter map[string]any) ([]vector.Hit, error) {
	m.mu.RLock()
	col, ok := m.collections[collection]
	if !ok {
		m.mu.RUnlock()
		return nil, nil
	}
	hits := make([]vector.Hit, 0, len(col))
	for _, p := range col {
		if !vector.MatchPayload(p.Payload, filter) {
			continue
		}
		score := cosine(query, p.Vector)
		hits = append(hits, vector.Hit{ID: p.ID, Score: score, Payload: p.Payload})
	}
	m.mu.RUnlock()
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// Delete removes a point by id. Missing ids are not an error.
func (m *MemIndex) Delete(_ context.Context, collection, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if col, ok := m.collections[collection]; ok {
		delete(col, id)
	}
	return nil
}

func cosine(a, b []float32) float32 {
	n := len(a)
	if n > len(b) {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (sqrt(na) * sqrt(nb)))
}

func sqrt(x float64) float64 {
	// Newton-Raphson; good enough for cosine similarity normalization.
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 16; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// NoopIndex satisfies IVectorIndex by always returning vector.ErrUnavailable.
// Use it when the index should be disabled (e.g. CI / offline) — the memory
// service catches the error and degrades to keyword search.
type NoopIndex struct{}

func (NoopIndex) Ensure(context.Context, string, int) error { return vector.ErrUnavailable }
func (NoopIndex) Upsert(context.Context, string, []vector.Point) error {
	return vector.ErrUnavailable
}

func (NoopIndex) Search(context.Context, string, []float32, int, map[string]any) ([]vector.Hit, error) {
	return nil, vector.ErrUnavailable
}
func (NoopIndex) Delete(context.Context, string, string) error { return vector.ErrUnavailable }
