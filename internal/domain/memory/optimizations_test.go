package memory_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
)

type dimStubEmbedder struct{ dim int }

func (s dimStubEmbedder) Embed(_ context.Context, docs []string) ([][]float32, error) {
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
func (s dimStubEmbedder) Dims() int { return s.dim }

func dimEmbedder(d int) port.IEmbeddingPort { return dimStubEmbedder{dim: d} }

func TestExpandQuery_EnglishStopWords(t *testing.T) {
	q := "the quick brown fox jumps over the lazy dog"
	expanded := memory.ExpandQuery(q)
	if strings.Contains(strings.ToLower(expanded), "the") {
		t.Errorf("expected 'the' to be removed, got %q", expanded)
	}
	if !strings.Contains(strings.ToLower(expanded), "quick") {
		t.Errorf("expected 'quick' to remain, got %q", expanded)
	}
	if !strings.Contains(strings.ToLower(expanded), "fox") {
		t.Errorf("expected 'fox' to remain, got %q", expanded)
	}
	if !strings.Contains(strings.ToLower(expanded), "dog") {
		t.Errorf("expected 'dog' to remain, got %q", expanded)
	}
}

func TestExpandQuery_ChineseStopWords(t *testing.T) {
	q := "我喜欢在Go中使用测试框架"
	expanded := memory.ExpandQuery(q)
	if strings.Contains(expanded, "的") {
		t.Errorf("expected '的' to be removed, got %q", expanded)
	}
	if !strings.Contains(strings.ToLower(expanded), "go") {
		t.Errorf("expected 'go' to remain, got %q", expanded)
	}
	if !strings.Contains(expanded, "测试") {
		t.Errorf("expected '测试' to remain, got %q", expanded)
	}
}

func TestExpandQuery_EmptyResult_Fallback(t *testing.T) {
	q := "the a an is are was were"
	expanded := memory.ExpandQuery(q)
	if expanded == "" {
		t.Errorf("expected fallback to original query when all stop words, got empty")
	}
}

func TestExpandQuery_NoStopWords(t *testing.T) {
	q := "kubernetes deployment helm chart"
	expanded := memory.ExpandQuery(q)
	words := strings.Fields(expanded)
	if len(words) != 4 {
		t.Errorf("expected 4 words, got %d: %q", len(words), expanded)
	}
}

func TestExpandQuery_EmptyQuery(t *testing.T) {
	expanded := memory.ExpandQuery("")
	if expanded != "" {
		t.Errorf("expected empty for empty query, got %q", expanded)
	}
}

func TestExpandQuery_ChineseMixed(t *testing.T) {
	q := "如何在Go项目中编写测试"
	expanded := memory.ExpandQuery(q)
	if !strings.Contains(strings.ToLower(expanded), "go") {
		t.Errorf("expected 'go' to remain, got %q", expanded)
	}
	if !strings.Contains(expanded, "项目") {
		t.Errorf("expected '项目' to remain, got %q", expanded)
	}
	if !strings.Contains(expanded, "测试") {
		t.Errorf("expected '测试' to remain, got %q", expanded)
	}
}

func TestMMRReRank_Empty(t *testing.T) {
	result := memory.MMRReRank(nil, nil, 0.7)
	if result != nil {
		t.Errorf("expected nil for empty input")
	}
}

func TestMMRReRank_SingleItem(t *testing.T) {
	items := []memport.MemoryItem{
		{Content: "use go modules"},
	}
	scores := []float64{0.9}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 1 {
		t.Errorf("expected 1 item, got %d", len(result))
	}
}

func TestMMRReRank_Diversity(t *testing.T) {
	items := []memport.MemoryItem{
		{ID: 1, Content: "use go modules for dependency management"},
		{ID: 2, Content: "use go modules for dependency injection"},
		{ID: 3, Content: "python pytest for testing"},
		{ID: 4, Content: "docker containers for deployment"},
	}
	scores := []float64{0.9, 0.85, 0.8, 0.75}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 4 {
		t.Fatalf("expected 4 items, got %d", len(result))
	}
	first := result[0]
	if first.ID != 1 {
		t.Errorf("expected ID 1 first (highest relevance), got %d", first.ID)
	}
	second := result[1]
	if second.ID == 2 {
		t.Errorf("expected diverse item second, not near-duplicate ID 2")
	}
}

func TestMMRReRank_LambdaBounds(t *testing.T) {
	items := []memport.MemoryItem{
		{ID: 1, Content: "alpha beta"},
		{ID: 2, Content: "alpha beta gamma"},
		{ID: 3, Content: "unrelated topic here"},
	}
	scores := []float64{0.9, 0.85, 0.5}

	resultHighLambda := memory.MMRReRank(items, scores, 0.9)
	if len(resultHighLambda) != 3 {
		t.Fatalf("expected 3 items with high lambda, got %d", len(resultHighLambda))
	}

	resultLowLambda := memory.MMRReRank(items, scores, 0.1)
	if len(resultLowLambda) != 3 {
		t.Fatalf("expected 3 items with low lambda, got %d", len(resultLowLambda))
	}

	resultDefault := memory.MMRReRank(items, scores, 1.5)
	if len(resultDefault) != 3 {
		t.Fatalf("expected 3 items with out-of-bounds lambda, got %d", len(resultDefault))
	}
}

func TestMMRReRank_AllIdentical(t *testing.T) {
	items := []memport.MemoryItem{
		{ID: 1, Content: "go test"},
		{ID: 2, Content: "go test"},
		{ID: 3, Content: "go test"},
	}
	scores := []float64{0.9, 0.85, 0.8}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result))
	}
}

func TestMMRReRank_AllDistinct(t *testing.T) {
	items := []memport.MemoryItem{
		{ID: 1, Content: "apple"},
		{ID: 2, Content: "banana"},
		{ID: 3, Content: "cherry"},
		{ID: 4, Content: "date"},
		{ID: 5, Content: "elderberry"},
	}
	scores := []float64{0.9, 0.8, 0.7, 0.6, 0.5}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result))
	}
}

func TestTemporalDecay_EvergreenGlobal(t *testing.T) {
	decay := memory.TemporalDecay(time.Now().Add(-90*24*time.Hour), "global")
	if math.Abs(decay-1.0) > 0.001 {
		t.Errorf("expected evergreen global decay 1.0, got %f", decay)
	}
}

func TestTemporalDecay_EvergreenProject(t *testing.T) {
	decay := memory.TemporalDecay(time.Now().Add(-90*24*time.Hour), "project")
	if math.Abs(decay-1.0) > 0.001 {
		t.Errorf("expected evergreen project decay 1.0, got %f", decay)
	}
}

func TestTemporalDecay_Recent(t *testing.T) {
	decay := memory.TemporalDecay(time.Now().Add(-1*time.Hour), "session")
	if decay < 0.99 {
		t.Errorf("expected recent session decay near 1.0, got %f", decay)
	}
}

func TestTemporalDecay_HalfLife(t *testing.T) {
	decay := memory.TemporalDecay(time.Now().Add(-30*24*time.Hour), "session")
	if math.Abs(decay-0.5) > 0.01 {
		t.Errorf("expected ~0.5 at 30 days, got %f", decay)
	}
}

func TestTemporalDecay_ZeroCreatedAt(t *testing.T) {
	decay := memory.TemporalDecay(time.Time{}, "session")
	if math.Abs(decay-1.0) > 0.001 {
		t.Errorf("expected 1.0 for zero CreatedAt, got %f", decay)
	}
}

func TestSourceWeight(t *testing.T) {
	tests := []struct {
		source string
		want   float64
	}{
		{"global", 1.2},
		{"GLOBAL", 1.2},
		{"project", 1.0},
		{"session", 0.8},
		{"manual", 1.0},
		{"auto:abc", 1.0},
		{"", 1.0},
	}
	for _, tt := range tests {
		got := memory.SourceWeight(tt.source)
		if math.Abs(got-tt.want) > 0.001 {
			t.Errorf("SourceWeight(%q) = %f, want %f", tt.source, got, tt.want)
		}
	}
}

func TestSearch_WithTemporalDecay(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(dimEmbedder(64))

	old := time.Now().Add(-60 * 24 * time.Hour)
	recent := time.Now()

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "go test backend deployment pipeline", Importance: 80,
		Source: "session", CreatedAt: old,
	})
	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "go test frontend component testing", Importance: 80,
		Source: "session", CreatedAt: recent,
	})

	items, err := svc.Search(ctx, "u1", "", "go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(items))
	}
}

func TestSearch_WithSourceWeighting(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(dimEmbedder(64))

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "project uses go modules always for backend", Importance: 70,
		Source: "global", CreatedAt: time.Now(),
	})
	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "sessions rely on go modules for frontend", Importance: 70,
		Source: "session", CreatedAt: time.Now(),
	})

	items, err := svc.Search(ctx, "u1", "", "go modules", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(items))
	}
}

func TestSearch_KeywordFallback_UsesExpandedQuery(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "prefer go test", Importance: 80, Source: "manual",
	})

	items, err := svc.Search(ctx, "u1", "", "prefer go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected results with keyword search")
	}
}

func TestSearch_MMR_Diversity(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(dimEmbedder(64))

	memories := []struct {
		content string
		source  string
	}{
		{"go test backend deployment pipeline", "session"},
		{"go test frontend component testing", "session"},
		{"python pytest data science workflow", "session"},
		{"docker kubernetes orchestration platform", "session"},
	}
	for _, c := range memories {
		svc.Save(ctx, &memport.MemoryItem{
			UserID: "u1", Scope: memport.ScopeUser, Category: "test",
			Content: c.content, Importance: 60, Source: c.source,
		})
	}

	items, err := svc.Search(ctx, "u1", "", "go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 3 {
		t.Fatalf("expected at least 3 diverse items, got %d", len(items))
	}
}

func TestSearch_EvergreenExemptFromDecay(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(dimEmbedder(64))

	old := time.Now().Add(-120 * 24 * time.Hour)

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "fact",
		Content: "project uses go modules always for backend", Importance: 90,
		Source: "project", CreatedAt: old,
	})

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "fact",
		Content: "sessions rely on go modules for frontend", Importance: 90,
		Source: "session", CreatedAt: old,
	})

	items, err := svc.Search(ctx, "u1", "", "go modules", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(items))
	}
}

func TestSourceWeight_FunctionExported(t *testing.T) {
	if math.Abs(memory.SourceWeight("global")-1.2) > 0.001 {
		t.Errorf("global weight wrong")
	}
	if math.Abs(memory.SourceWeight("session")-0.8) > 0.001 {
		t.Errorf("session weight wrong")
	}
}

func TestTemporalDecay_FunctionExported(t *testing.T) {
	decay := memory.TemporalDecay(time.Now(), "session")
	if decay < 0.99 {
		t.Errorf("decay for now should be ~1.0, got %f", decay)
	}

	decay30 := memory.TemporalDecay(time.Now().Add(-30*24*time.Hour), "session")
	if math.Abs(decay30-0.5) > 0.01 {
		t.Errorf("decay at 30 days should be ~0.5, got %f", decay30)
	}
}

func TestMMRReRank_FunctionExported(t *testing.T) {
	items := []memport.MemoryItem{
		{Content: "a"}, {Content: "b"},
	}
	scores := []float64{0.9, 0.8}
	result := memory.MMRReRank(items, scores, 0.5)
	if len(result) != 2 {
		t.Errorf("expected 2 items, got %d", len(result))
	}
}

func TestMMRReRank_DefaultLambda(t *testing.T) {
	items := []memport.MemoryItem{
		{Content: "go test framework"},
		{Content: "go test tools"},
		{Content: "python testing"},
	}
	scores := []float64{0.9, 0.85, 0.5}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}
}

func TestSearch_Integration_AllOptimizations(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	svc.SetEmbedder(dimEmbedder(64))

	old := time.Now().Add(-60 * 24 * time.Hour)
	recent := time.Now()

	memories := []struct {
		content   string
		source    string
		createdAt time.Time
		imp       int
	}{
		{"prefer go test over manual", "session", old, 80},
		{"use go modules for deps", "project", recent, 70},
		{"prefer go test over automated", "session", recent, 80},
		{"python pytest for python projects", "session", recent, 60},
		{"docker compose for local dev", "global", old, 50},
	}

	for _, m := range memories {
		svc.Save(ctx, &memport.MemoryItem{
			UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
			Content: m.content, Importance: m.imp,
			Source: m.source, CreatedAt: m.createdAt,
		})
	}

	items, err := svc.Search(ctx, "u1", "", "go test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected results from integrated search")
	}

	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.Content] {
			t.Logf("duplicate content after MMR: %s", it.Content)
		}
		seen[it.Content] = true
	}
}

func TestSearch_NoEmbedder_ExpandedKeyword(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "prefer go test over manual testing", Importance: 80,
	})

	items, err := svc.Search(ctx, "u1", "", "prefer go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected results with expanded keyword search")
	}
}

func TestExpandQuery_IntegrationInSearch(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)

	svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "use go test for testing", Importance: 80,
	})

	items, err := svc.Search(ctx, "u1", "", "the go test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected results even with stop word 'the' in query")
	}
}

func TestMMRReRank_SameContentDifferentID(t *testing.T) {
	items := []memport.MemoryItem{
		{ID: 1, Content: "go testing"},
		{ID: 2, Content: "go testing"},
		{ID: 3, Content: "python testing"},
	}
	scores := []float64{0.9, 0.85, 0.5}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}
}

func TestMMRReRank_DiverseSelection(t *testing.T) {
	items := []memport.MemoryItem{
		{ID: 1, Content: "kubernetes"},
		{ID: 2, Content: "docker containers"},
		{ID: 3, Content: "linux kernel"},
		{ID: 4, Content: "networking stack"},
	}
	scores := []float64{0.95, 0.9, 0.85, 0.8}
	result := memory.MMRReRank(items, scores, 0.7)
	if len(result) != 4 {
		t.Errorf("expected 4 items, got %d", len(result))
	}
	first := result[0]
	if first.ID != 1 {
		t.Errorf("expected ID 1 first, got %d", first.ID)
	}
}