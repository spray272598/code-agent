package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tenant"
	"github.com/spray272598/code-agent/internal/domain/vector"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
	vectorinfra "github.com/spray272598/code-agent/internal/infrastructure/vector"
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

// repoVector returns a fresh in-process vector index.
func repoVector() vector.IVectorIndex { return vectorinfra.NewMemIndex() }

func TestSearch_UsesVectorWhenAvailable(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(repoVector(), "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Category: "pref",
		Content: "go test preferred", Importance: 80,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "bob", Scope: memport.ScopeUser, Category: "pref",
		Content: "python pytest preferred", Importance: 80,
	}); err != nil {
		t.Fatal(err)
	}

	items, err := svc.Search(ctx, "alice", "", "go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one hit")
	}
	if items[0].UserID != "alice" {
		t.Fatalf("first hit must belong to alice, got %q", items[0].UserID)
	}
}

func TestSearchCtx_RequiresTenant(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)

	_, err := svc.SearchCtx(ctx, "", "anything", 5)
	if !errors.Is(err, memory.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}

	ctx2 := tenant.With(ctx, tenant.Tenant{UserID: "alice"})
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(repoVector(), "memories")
	if err := svc.Save(ctx2, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Category: "x",
		Content: "go modules rocks", Importance: 70,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.SearchCtx(ctx2, "", "go modules", 5)
	if err != nil {
		t.Fatalf("SearchCtx: %v", err)
	}
	if len(items) == 0 || items[0].UserID != "alice" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestSearch_VectorFilterStrict(t *testing.T) {
	ctx := context.Background()
	idx := repoVector()
	svc := memory.NewService(repository.NewMemoryCoreRepo())
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(idx, "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Content: "alpha bravo charlie", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "bob", Scope: memport.ScopeUser, Content: "alpha bravo charlie", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	hits, _ := idx.Search(ctx, "memories", []float32{1, 1, 1, 1}, 10, map[string]any{"user_id": "alice"})
	if len(hits) == 0 {
		t.Fatalf("expected hits")
	}
	for _, h := range hits {
		if got := h.Payload["user_id"]; got != "alice" {
			t.Fatalf("filter leaked: hit %+v", h)
		}
	}
}

func TestFindDuplicate_PrefersVector(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(repoVector(), "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Content: "use go modules", Importance: 60,
	}); err != nil {
		t.Fatal(err)
	}
	// Identical content → dedupe via vector.
	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Content: "use go modules", Importance: 60,
	}); err != nil {
		t.Fatal(err)
	}
	items, _ := svc.List(ctx, "alice", "", memport.ScopeUser, 50)
	if len(items) != 1 {
		t.Fatalf("expected dedupe to keep 1, got %d", len(items))
	}
}

func TestBackfillVector_IndexesMemories(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	// Save WITHOUT embedder/vector so items persist without embeddings.
	for _, txt := range []string{"first memory", "second memory", "third memory"} {
		if err := svc.Save(ctx, &memport.MemoryItem{
			UserID: "alice", Scope: memport.ScopeUser, Content: txt, Importance: 50,
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
	hits, _ := idx.Search(ctx, "memories", []float32{1, 1, 1, 1}, 10, map[string]any{"user_id": "alice"})
	if len(hits) == 0 {
		t.Fatalf("expected hits after backfill, got 0")
	}
}

func TestSearch_NoVector_KeywordFallback(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Content: "go test is fine", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.Search(ctx, "alice", "", "go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("keyword fallback failed")
	}
}

func TestSearch_NoopVector_KeywordFallback(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(vectorinfra.NoopIndex{}, "memories")
	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Content: "kubernetes everywhere", Importance: 50,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.Search(ctx, "alice", "", "kubernetes", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected fallback to succeed with NoopIndex")
	}
}

func TestMemorySave_UpsertsVector(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	idx := repoVector()
	svc.SetEmbedder(memEmbedder())
	svc.SetVectorIndex(idx, "memories")

	if err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "alice", Scope: memport.ScopeUser, Content: strings.Repeat("x", 8), Importance: 60,
	}); err != nil {
		t.Fatal(err)
	}
	hits, _ := idx.Search(ctx, "memories", []float32{1, 1, 1, 1}, 5, map[string]any{"user_id": "alice"})
	if len(hits) != 1 {
		t.Fatalf("expected 1 vector entry, got %d", len(hits))
	}
	if hits[0].Payload["user_id"] != "alice" {
		t.Fatalf("payload missing user_id: %+v", hits[0].Payload)
	}
}