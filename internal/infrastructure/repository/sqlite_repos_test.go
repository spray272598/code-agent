package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/spray272598/code-agent/internal/domain/audit"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/infrastructure/sqlite"
)

// openRepoTestSQLite opens a file-backed SQLite DB (same driver as the
// production bootstrap) and creates the schema for the repos under test.
// It never touches the user-visible ./data/code-agent.db instance.
//
// The schema comes from the production migrator rather than a local copy: a
// hand-maintained duplicate drifts silently (core_memory once lost created_at
// here) and then fails with an opaque "no such column" far from the real cause.
func openRepoTestSQLite(t *testing.T) *sql.DB {
	t.Helper()
	path := t.TempDir() + "/repos.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestSQLiteSessionRepo_RoundTrip(t *testing.T) {
	db := openRepoTestSQLite(t)
	repo := NewSQLiteSessionRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := &model.Session{
		ID: "s1", UserID: "u1", ProjectID: "p1", AgentID: "a1",
		Title: "t", Status: "active", MessageCount: 7, TokenUsed: 1234,
		WorkingDir: "/tmp/wd", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByID(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.ID != s.ID || got.UserID != s.UserID || got.ProjectID != s.ProjectID ||
		got.AgentID != s.AgentID || got.Title != s.Title || got.Status != s.Status ||
		got.MessageCount != s.MessageCount || got.TokenUsed != s.TokenUsed || got.WorkingDir != s.WorkingDir {
		t.Fatalf("field mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(s.CreatedAt) || !got.UpdatedAt.Equal(s.UpdatedAt) {
		t.Fatalf("time mismatch: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}

	list, err := repo.ListByUser(ctx, "u1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "s1" {
		t.Fatalf("ListByUser: %+v", list)
	}
	// default limit applied when <=0
	list2, err := repo.ListByUser(ctx, "u1", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list2) != 1 {
		t.Fatalf("ListByUser default limit: %+v", list2)
	}
}

func TestSQLiteMessageRepo_RoundTrip(t *testing.T) {
	db := openRepoTestSQLite(t)
	repo := NewSQLiteMessageRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 0; i < 3; i++ {
		m := &model.Message{
			ID: "m" + string(rune('0'+i)), SessionID: "s1", Role: "user",
			Content: "c" + string(rune('0'+i)), ToolName: "bash", ToolCallID: "tc" + string(rune('0'+i)),
			Step: i, TokenCount: 10 + i, Priority: 1, CreatedAt: now,
		}
		if err := repo.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.ListBySession(ctx, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3, got %d", len(list))
	}
	if list[0].Role != "user" || list[0].ToolName != "bash" || list[0].Priority != 1 {
		t.Fatalf("field mismatch: %+v", list[0])
	}
	maps, err := repo.ListAsMaps(ctx, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) != 3 {
		t.Fatalf("ListAsMaps want 3, got %d", len(maps))
	}
	if maps[0]["role"] != "user" || maps[0]["toolName"] != "bash" || maps[0]["step"] != 0 {
		t.Fatalf("map mismatch: %+v", maps[0])
	}
}

func TestSQLiteSummaryRepo_RoundTrip(t *testing.T) {
	db := openRepoTestSQLite(t)
	repo := NewSQLiteSummaryRepo(db)
	ctx := context.Background()

	if err := repo.Save(ctx, "s1", "the summary", 42); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "the summary" {
		t.Fatalf("want 'the summary', got %q", got)
	}
	// missing session returns empty, no error
	miss, err := repo.Get(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if miss != "" {
		t.Fatalf("want empty, got %q", miss)
	}
}

func TestSQLiteMemoryRepo_RoundTrip(t *testing.T) {
	db := openRepoTestSQLite(t)
	repo := NewSQLiteMemoryRepo(db)
	ctx := context.Background()

	item := &memport.MemoryItem{
		ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "c", Content: "hello world", Importance: 5, Source: "test",
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatal(err)
	}
	if item.ID == 0 {
		t.Fatal("expected autoincrement ID to be set")
	}

	// update path: same ID, changed content
	item.Content = "hello updated"
	item.Importance = 8
	if err := repo.Save(ctx, item); err != nil {
		t.Fatal(err)
	}

	list, err := repo.List(ctx, "p1", memport.ScopeProject, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Content != "hello updated" || list[0].Importance != 8 {
		t.Fatalf("List mismatch: %+v", list)
	}

	hits, err := repo.Search(ctx, "p1", "hello", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search want 1, got %d", len(hits))
	}
	none, err := repo.Search(ctx, "p1", "zzz", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("Search zzz want 0, got %d", len(none))
	}

	// Prune removes low-importance items (<10); our item is 8 -> removed
	n, err := repo.Prune(ctx, 10, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Prune want 1, got %d", n)
	}
	after, _ := repo.List(ctx, "p1", memport.ScopeProject, 0)
	if len(after) != 0 {
		t.Fatalf("after prune want 0, got %d", len(after))
	}

	// re-insert then Delete by id
	item2 := &memport.MemoryItem{Scope: memport.ScopeUser, Content: "x", Importance: 1}
	if err := repo.Save(ctx, item2); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, item2.ID); err != nil {
		t.Fatal(err)
	}
	noEmbed, err := repo.ListNoEmbedding(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(noEmbed) != 0 {
		t.Fatalf("ListNoEmbedding want 0 (no nil embeddings stored), got %d", len(noEmbed))
	}
}

func TestSQLiteAuditRepo_RoundTrip(t *testing.T) {
	db := openRepoTestSQLite(t)
	repo := NewSQLiteAuditRepo(db)
	ctx := context.Background()

	e := audit.Entry{
		UserID: "u1", SessionID: "s1", Action: "tool_call", Tool: "bash",
		Detail: "ran", Decision: "ok", LatencyMs: 5,
	}
	if err := repo.Append(ctx, e); err != nil {
		t.Fatal(err)
	}

	bySession, err := repo.ListBySession(ctx, "u1", "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bySession) != 1 || bySession[0].Tool != "bash" || bySession[0].Decision != "ok" {
		t.Fatalf("ListBySession mismatch: %+v", bySession)
	}

	// single-operator form: ListForUser delegates to ListBySession for the
	// given session and matches every actor regardless of operator identity.
	byUser, err := repo.ListForUser(ctx, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 1 {
		t.Fatalf("ListForUser want 1, got %d", len(byUser))
	}
}
