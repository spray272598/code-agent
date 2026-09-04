package memory_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/vector"
)

// stubEmbedder implements port.IEmbeddingPort. Vectors are deterministic so we
// can assert ranking; identical inputs yield identical vectors so duplicate
// detection triggers.
type stubEmbedder struct{ dim int }

func (s stubEmbedder) Embed(_ context.Context, docs []string) ([][]float32, error) {
	out := make([][]float32, len(docs))
	for i, d := range docs {
		v := make([]float32, s.dim)
		for _, c := range d {
			v[int(c)%s.dim] += 1
		}
		out[i] = v
	}
	return out, nil
}
func (s stubEmbedder) Dims() int { return s.dim }

// memEmbedder builds a 4-dim stub embedder for tests.
func memEmbedder() port.IEmbeddingPort { return stubEmbedder{dim: 4} }

// memVectorIndex is an in-memory vector index for testing.
type memVectorIndex struct {
	mu          sync.RWMutex
	collections map[string]map[string]vector.Point
	dims        int
}

func newMemVectorIndex(dims int) *memVectorIndex {
	return &memVectorIndex{collections: make(map[string]map[string]vector.Point), dims: dims}
}

func (m *memVectorIndex) Ensure(_ context.Context, collection string, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.collections[collection] == nil {
		m.collections[collection] = make(map[string]vector.Point)
	}
	return nil
}

func (m *memVectorIndex) Upsert(_ context.Context, collection string, points []vector.Point) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.collections[collection] == nil {
		m.collections[collection] = make(map[string]vector.Point)
	}
	for _, p := range points {
		m.collections[collection][p.ID] = p
	}
	return nil
}

func (m *memVectorIndex) Search(_ context.Context, collection string, query []float32, topK int, filter map[string]any) ([]vector.Hit, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.collections[collection]
	if items == nil {
		return nil, nil
	}
	type scored struct {
		id    string
		score float32
	}
	var results []scored
	for id, pt := range items {
		// Apply filter
		if filter != nil && !vector.MatchPayload(pt.Payload, filter) {
			continue
		}
		var score float32
		for i := 0; i < len(query) && i < len(pt.Vector); i++ {
			score += query[i] * pt.Vector[i]
		}
		results = append(results, scored{id: id, score: score})
	}
	// Sort by score descending
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if topK > len(results) {
		topK = len(results)
	}
	var out []vector.Hit
	for _, r := range results[:topK] {
		pt := items[r.id]
		out = append(out, vector.Hit{ID: r.id, Score: r.score, Payload: pt.Payload})
	}
	return out, nil
}

func (m *memVectorIndex) Delete(_ context.Context, collection string, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.collections[collection] != nil {
		delete(m.collections[collection], id)
	}
	return nil
}

// memCoreRepo is an in-memory core repository for testing.
type memCoreRepo struct {
	mu       sync.RWMutex
	memories map[int64]*memport.MemoryItem
	nextID   int64
}

func newMemCoreRepo() *memCoreRepo {
	return &memCoreRepo{memories: make(map[int64]*memport.MemoryItem)}
}

func (r *memCoreRepo) Save(_ context.Context, item *memport.MemoryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item.ID == 0 {
		r.nextID++
		item.ID = r.nextID
	}
	r.memories[item.ID] = item
	return nil
}

func (r *memCoreRepo) List(_ context.Context, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []memport.MemoryItem
	for _, item := range r.memories {
		if (projectID == "" || item.ProjectID == projectID) && (scope == "" || item.Scope == scope) {
			out = append(out, *item)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *memCoreRepo) Search(_ context.Context, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []memport.MemoryItem
	for _, item := range r.memories {
		if (projectID == "" || item.ProjectID == projectID) && strings.Contains(item.Content, query) {
			out = append(out, *item)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *memCoreRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.memories, id)
	return nil
}

func (r *memCoreRepo) ListNoEmbedding(_ context.Context, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []memport.MemoryItem
	for _, item := range r.memories {
		if len(item.Embedding) == 0 {
			out = append(out, *item)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *memCoreRepo) Prune(_ context.Context, _ int, _ time.Time) (int, error) {
	return 0, nil
}

func repoVector() vector.IVectorIndex { return newMemVectorIndex(4) }

// noopVectorIndex is a no-op vector index for testing.
type noopVectorIndex struct{}

func (n *noopVectorIndex) Ensure(_ context.Context, _ string, _ int) error   { return nil }
func (n *noopVectorIndex) Upsert(_ context.Context, _ string, _ []vector.Point) error {
	return nil
}
func (n *noopVectorIndex) Search(_ context.Context, _ string, _ []float32, _ int, _ map[string]any) ([]vector.Hit, error) {
	return nil, nil
}
func (n *noopVectorIndex) Delete(_ context.Context, _ string, _ string) error { return nil }

func TestSearch_UsesVectorWhenAvailable(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(repoVector(), "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Category: "pref",
		Content: "go test preferred", Importance: 80,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "other", Scope: memport.ScopeUser, Category: "pref",
		Content: "python pytest preferred", Importance: 80,
	}); err != nil {
		t.Fatal(err)
	}

	items, err := svc.Search(ctx, "proj", "go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one hit")
	}
	if !strings.Contains(items[0].Content, "go test") {
		t.Fatalf("first hit should be the go-test memory, got %q", items[0].Content)
	}
}

func TestSearchCtx_RequiresProject(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)

	_, err := svc.SearchCtx(ctx, "", "anything", 5)
	if !errors.Is(err, memory.ErrProjectMissing) {
		t.Fatalf("expected ErrProjectMissing, got %v", err)
	}

	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(repoVector(), "memories")
	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Category: "x",
		Content: "go modules rocks", Importance: 70,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.SearchCtx(ctx, "proj", "go modules", 5)
	if err != nil {
		t.Fatalf("SearchCtx: %v", err)
	}
	if len(items) == 0 || items[0].ProjectID != "proj" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestSearch_VectorFilterStrict(t *testing.T) {
	ctx := context.Background()
	idx := repoVector()
	svc := memory.NewService(newMemCoreRepo())
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(idx, "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "alice", Scope: memport.ScopeUser, Content: "alpha bravo charlie", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "bob", Scope: memport.ScopeUser, Content: "alpha bravo charlie", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	hits, _ := idx.Search(ctx, "memories", []float32{1, 1, 1, 1}, 10, map[string]any{"project_id": "alice"})
	if len(hits) == 0 {
		t.Fatalf("expected hits")
	}
	for _, h := range hits {
		if got := h.Payload["project_id"]; got != "alice" {
			t.Fatalf("filter leaked: hit %+v", h)
		}
	}
}

func TestFindDuplicate_PrefersVector(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(repoVector(), "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Content: "use go modules", Importance: 60,
	}); err != nil {
		t.Fatal(err)
	}
	// Identical content → dedupe via vector.
	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Content: "use go modules", Importance: 60,
	}); err != nil {
		t.Fatal(err)
	}
	items, _ := svc.List(ctx, "proj", memport.ScopeUser, 50)
	if len(items) != 1 {
		t.Fatalf("expected dedupe to keep 1, got %d", len(items))
	}
}

func TestBackfillVector_IndexesMemories(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)
	// Save WITHOUT embedder/vector so items persist without embeddings.
	for _, txt := range []string{"first memory", "second memory", "third memory"} {
		if err := svc.Save(ctx, &memport.MemoryItem{
			ProjectID: "proj", Scope: memport.ScopeUser, Content: txt, Importance: 50,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Now wire the embedder + vector and backfill.
	svc.SetEmbedder(memEmbedder())
	idx := repoVector()
	svc.SetVectorIndex(idx, "memories")
	if n := svc.BackfillVector(ctx, 50); n < 1 {
		t.Fatalf("BackfillVector returned %d, expected ≥1", n)
	}
	hits, _ := idx.Search(ctx, "memories", []float32{1, 1, 1, 1}, 10, map[string]any{"project_id": "proj"})
	if len(hits) == 0 {
		t.Fatalf("expected hits after backfill, got 0")
	}
}

func TestSearch_NoVector_KeywordFallback(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Content: "go test is fine", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.Search(ctx, "proj", "go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("keyword fallback failed")
	}
}

func TestSearch_NoopVector_KeywordFallback(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(&noopVectorIndex{}, "memories")
	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Content: "kubernetes everywhere", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.Search(ctx, "proj", "kubernetes", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected fallback to succeed with NoopIndex")
	}
}

func TestMemorySave_UpsertsVector(t *testing.T) {
	ctx := context.Background()
	repo := newMemCoreRepo()
	svc := memory.NewService(repo)
	idx := repoVector()
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(idx, "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "proj", Scope: memport.ScopeUser, Content: strings.Repeat("x", 8), Importance: 60,
	}); err != nil {
		t.Fatal(err)
	}
	hits, _ := idx.Search(ctx, "memories", []float32{1, 1, 1, 1}, 5, map[string]any{"project_id": "proj"})
	if len(hits) != 1 {
		t.Fatalf("expected 1 vector entry, got %d", len(hits))
	}
	if hits[0].Payload["project_id"] != "proj" {
		t.Fatalf("payload missing project_id: %+v", hits[0].Payload)
	}
}
