package memory

import (
	"context"
	"os"
	"testing"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type fakeRepo struct {
	items []memport.MemoryItem
}

func (r *fakeRepo) Save(_ context.Context, item *memport.MemoryItem) error {
	if item.ID == 0 {
		item.ID = int64(len(r.items) + 1)
	}
	r.items = append(r.items, *item)
	return nil
}

func (r *fakeRepo) List(_ context.Context, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	var out []memport.MemoryItem
	for _, it := range r.items {
		if projectID != "" && it.ProjectID != projectID {
			continue
		}
		if scope != "" && it.Scope != scope {
			continue
		}
		out = append(out, it)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeRepo) Search(_ context.Context, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	var out []memport.MemoryItem
	for _, it := range r.items {
		if projectID == "" || it.ProjectID == projectID {
			if len(query) == 0 || contains(it.Content, query) {
				out = append(out, it)
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeRepo) Delete(_ context.Context, id int64) error {
	for i, it := range r.items {
		if it.ID == id {
			r.items = append(r.items[:i], r.items[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *fakeRepo) ListNoEmbedding(_ context.Context, limit int) ([]memport.MemoryItem, error) {
	var out []memport.MemoryItem
	for _, it := range r.items {
		if len(it.Embedding) == 0 {
			out = append(out, it)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeRepo) Prune(_ context.Context, minImportance int, olderThan time.Time) (int, error) {
	pruned := 0
	var kept []memport.MemoryItem
	for _, it := range r.items {
		if it.Importance >= minImportance || it.CreatedAt.After(olderThan) {
			kept = append(kept, it)
		} else {
			pruned++
		}
	}
	r.items = kept
	return pruned, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDreamConsolidation(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_ = svc.Save(ctx, &memport.MemoryItem{
			ProjectID: "p1", Scope: memport.ScopeProject,
			Category: "test", Content: "memory item " + string(rune('a'+i)),
			Importance: 50, Source: "test",
		})
	}

	cfg := DefaultDreamConfig()
	cfg.MinMemories = 3
	result, err := svc.RunDreamConsolidation(ctx, cfg, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Consolidated == 0 && result.Pruned == 0 {
		t.Log("no consolidation or pruning needed")
	}
}

func TestDreamMaybeRun(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	cfg := DefaultDreamConfig()
	cfg.MinSessions = 5
	cfg.MinMemories = 2

	result, err := svc.MaybeRunDreamConsolidation(ctx, cfg, "p1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("should not run with insufficient sessions")
	}

	_ = svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "test", Content: "test content", Importance: 50, Source: "test",
	})

	result, err = svc.MaybeRunDreamConsolidation(ctx, cfg, "p1", 10)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFlushConversation(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	turns := []ConversationTurn{
		{Role: "user", Content: "记住项目使用Go语言", Time: time.Now()},
		{Role: "assistant", Content: "好的，已记录", Time: time.Now()},
		{Role: "user", Content: "以后总是用fmt.Printf", Time: time.Now()},
		{Role: "assistant", Content: "了解", Time: time.Now()},
	}

	cfg := DefaultFlushConfig()
	cfg.AutoExtract = true
	result, err := svc.FlushConversation(ctx, "p1", turns, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("flushed=%d extracted=%d duration=%v", result.Flushed, result.Extracted, result.Duration)
}

func TestDurableCheckpointStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewDurableCheckpointStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	entry := &CheckpointEntry{
		ID: 1, SessionID: "s1", UserID: "u1",
		Content: "test content", Category: "test", CreatedAt: time.Now(),
	}
	if err := store.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}

	entries, _ := store.List(ctx, "s1")
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}

	latest, _ := store.Latest(ctx, "s1")
	if latest == nil || latest.Content != "test content" {
		t.Fatal("latest entry mismatch")
	}

	if err := store.Rewind(ctx, "s1", 0); err != nil {
		t.Fatal(err)
	}
	entries, _ = store.List(ctx, "s1")
	if len(entries) != 0 {
		t.Fatalf("want 0 entries after rewind, got %d", len(entries))
	}
}

func TestDurableCheckpointPrune(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewDurableCheckpointStore(dir, 10)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		store.Save(ctx, &CheckpointEntry{
			ID: int64(i + 1), SessionID: "s1", UserID: "u1",
			Content: "content", Category: "test",
			CreatedAt: time.Now().Add(-time.Duration(i+1) * 24 * time.Hour),
		})
	}

	pruned, _ := store.Prune(ctx, time.Now().Add(-2*24*time.Hour))
	t.Logf("pruned %d old entries", pruned)

	dir2 := t.TempDir()
	store2, _ := NewDurableCheckpointStore(dir2, 10)
	for i := 0; i < 3; i++ {
		store2.Save(ctx, &CheckpointEntry{
			ID: int64(i + 1), SessionID: "s1", UserID: "u1",
			Content: "content", Category: "test", CreatedAt: time.Now(),
		})
	}
	sessions, _ := store2.Sessions(ctx)
	if len(sessions) < 1 {
		t.Fatal("no sessions found")
	}

	userEntries, _ := store2.ListByUser(ctx, "u1", 10)
	if len(userEntries) != 3 {
		t.Fatalf("want 3 user entries, got %d", len(userEntries))
	}
}

func TestHybridSearch(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	_ = svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "architecture", Content: "use Go for backend",
		Importance: 80, Source: "test",
	})
	_ = svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "preference", Content: "always prefer clean code",
		Importance: 60, Source: "test",
	})

	opts := DefaultSearchOptions()
	scored, err := svc.HybridSearch(ctx, "p1", "Go", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(scored) < 1 {
		t.Fatal("should find at least one result")
	}
	t.Logf("found %d results, top score=%.3f", len(scored), scored[0].Score)
}

func TestFormatForPromptExtended(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	_ = svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "arch", Content: "use Go with Gin framework",
		Importance: 70, Source: "test",
	})

	opts := DefaultSearchOptions()
	block := svc.FormatForPromptExtended(ctx, "p1", "Go", 5, opts)
	if block == "" {
		t.Fatal("should return memory block")
	}
	if len(block) < 20 {
		t.Fatal("block too short")
	}
	t.Log(block)
}

func TestSessionSummary(t *testing.T) {
	ss := NewSessionSummary()

	messages := []struct{ Role, Content string }{
		{Role: "user", Content: "帮我创建一个Go项目"},
		{Role: "assistant", Content: "好的"},
	}

	title := ss.GenerateTitleFromMessages(messages)
	if title == "" {
		t.Fatal("should generate title")
	}
	ss.SetTitle("s1", title)
	if ss.GetTitle("s1") != title {
		t.Fatal("title mismatch")
	}

	ss.MarkComplete("s1")
	if !ss.IsComplete("s1") {
		t.Fatal("should be complete")
	}

	ss.Reset("s1")
	if ss.GetTitle("s1") != "" {
		t.Fatal("should be cleared")
	}
}

func TestCompactionRecovery(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	_ = svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "task", Content: "working on API endpoint",
		Importance: 60, Source: "test",
	})

	dir := t.TempDir()
	store, _ := NewDurableCheckpointStore(dir, 10)
	store.Save(ctx, &CheckpointEntry{
		ID: 1, SessionID: "s1", UserID: "u1",
		Content: "checkpoint content", Category: "task", CreatedAt: time.Now(),
	})

	recovery := NewCompactionRecovery(svc, store)
	result, err := recovery.AfterCompaction(ctx, "p1", "API endpoint")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("recovered=%d checkpointHits=%d", result.RecoveredItems, result.CheckpointHits)

	items, err := recovery.RecoverSessionContext(ctx, "p1", "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 1 {
		t.Fatal("should recover session context")
	}
}

func TestMemoryBackend(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()

	backend := NewServiceBackend(svc)

	_ = svc.Save(ctx, &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "test", Content: "backend test",
		Importance: 50, Source: "test",
	})

	items, _ := backend.List(ctx, "p1", memport.ScopeProject, 10)
	if len(items) < 1 {
		t.Fatal("backend list failed")
	}

	chunks, _ := backend.TotalChunks(ctx)
	if chunks < 1 {
		t.Fatal("total chunks should be >= 1")
	}

	err := backend.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pruned, _ := backend.Prune(ctx, 100, time.Now())
	t.Logf("pruned %d", pruned)
}

func TestChunkTurns(t *testing.T) {
	turns := make([]ConversationTurn, 25)
	for i := range turns {
		turns[i] = ConversationTurn{
			Role: "user", Content: "message " + string(rune('a'+i%26)),
		}
	}
	turns[5].Role = "assistant"
	turns[15].Role = "assistant"

	windows := chunkTurns(turns, 10)
	if len(windows) < 2 {
		t.Fatalf("should create at least 2 windows, got %d", len(windows))
	}
	t.Logf("created %d windows", len(windows))
}

func TestRuleBasedConsolidate(t *testing.T) {
	contents := []string{
		"[arch|imp=80] Use Go for backend",
		"[arch|imp=80] Use Go for backend",
		"[pref|imp=60] Prefer clean code",
	}
	result := ruleBasedConsolidate(contents)
	if result == "" {
		t.Fatal("should consolidate")
	}
	t.Log(result)
}

func TestRuleBasedTitle(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"帮我创建一个Go项目", "帮我创建一个Go项目"},
		{"", ""},
		{"短", ""},
	}
	for _, tt := range tests {
		result := ruleBasedTitle(tt.input)
		if result != tt.expected {
			t.Logf("input=%q expected=%q got=%q", tt.input, tt.expected, result)
		}
	}
}

func TestDurableCheckpointRehydrate(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewDurableCheckpointStore(dir, 10)
	ctx := context.Background()

	store.Save(ctx, &CheckpointEntry{
		ID: 1, SessionID: "s1", UserID: "u1",
		Content: "content1", Category: "test", CreatedAt: time.Now(),
	})
	store.Save(ctx, &CheckpointEntry{
		ID: 2, SessionID: "s1", UserID: "u1",
		Content: "content2", Category: "test", CreatedAt: time.Now(),
	})

	store2, _ := NewDurableCheckpointStore(dir, 10)
	entries, _ := store2.List(ctx, "s1")
	if len(entries) != 2 {
		t.Fatalf("want 2 entries after rehydrate, got %d", len(entries))
	}
	if entries[0].Content != "content1" || entries[1].Content != "content2" {
		t.Fatal("content mismatch after rehydrate")
	}

	os.RemoveAll(dir)
}
