package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
)

func newTestSealer(t *testing.T) kms.CryptoSealer {
	t.Helper()
	dir := t.TempDir()
	prev := kmsinfra.EnvDir
	kmsinfra.EnvDir = func() string { return dir }
	t.Cleanup(func() { kmsinfra.EnvDir = prev })
	t.Setenv("CODE_AGENT_KMS_KEY", "")
	t.Setenv("CODE_AGENT_KMS_PREVIOUS", "")
	s, err := kmsinfra.NewSealer()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMemoryLLMKeyRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))

	k := llmkey.Key{
		UserID: "u1", Alias: "prod", Provider: "openai",
		APIKey: "sk-secret-1234567890", APIBase: "https://api.openai.com/v1", Enabled: true,
	}
	if err := repo.Save(ctx, k); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "u1", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != k.APIKey || got.APIBase != k.APIBase || got.Provider != k.Provider {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, k)
	}
}

func TestMemoryLLMKeyRepo_NoPlaintextLeak(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))
	const secret = "sk-must-not-leak-ABCDEF"
	if err := repo.Save(ctx, llmkey.Key{
		UserID: "u1", Alias: "p", Provider: "openai", APIKey: secret, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, alias := range repo.data["u1"] {
		if strings.Contains(alias.APIKeyCT, secret) {
			t.Fatalf("api key leaked into ciphertext storage: %q", alias.APIKeyCT)
		}
	}
}

func TestMemoryLLMKeyRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))
	for _, e := range []struct{ uid, alias, key string }{
		{"alice", "p", "alice-key"},
		{"bob", "p", "bob-key"},
	} {
		if err := repo.Save(ctx, llmkey.Key{
			UserID: e.uid, Alias: e.alias, Provider: "openai", APIKey: e.key,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := repo.Get(ctx, "alice", "p"); got.APIKey != "alice-key" {
		t.Fatalf("alice: got %q", got.APIKey)
	}
	if got, _ := repo.Get(ctx, "bob", "p"); got.APIKey != "bob-key" {
		t.Fatalf("bob: got %q", got.APIKey)
	}
	list, _ := repo.ListByUser(ctx, "alice")
	if len(list) != 1 || list[0].UserID != "alice" || list[0].APIKey != "alice-key" {
		t.Fatalf("alice list leak: %+v", list)
	}
}

func TestMemoryLLMKeyRepo_NotFound(t *testing.T) {
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))
	if _, err := repo.Get(context.Background(), "u1", "missing"); !errors.Is(err, llmkey.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryLLMKeyRepo_DuplicateRejected(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))
	k := llmkey.Key{UserID: "u1", Alias: "p", Provider: "openai", APIKey: "k1"}
	if err := repo.Save(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, k); !errors.Is(err, llmkey.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestMemoryLLMKeyRepo_TenantMissing(t *testing.T) {
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))
	if err := repo.Save(context.Background(), llmkey.Key{Alias: "p", Provider: "x", APIKey: "k"}); !errors.Is(err, llmkey.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}
}

func TestMemoryLLMKeyRepo_DeleteAndList(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryLLMKeyRepo(newTestSealer(t))
	for _, alias := range []string{"a", "b", "c"} {
		if err := repo.Save(ctx, llmkey.Key{
			UserID: "u1", Alias: alias, Provider: "openai", APIKey: alias + "-key",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Delete(ctx, "u1", "b"); err != nil {
		t.Fatal(err)
	}
	list, _ := repo.ListByUser(ctx, "u1")
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	for _, k := range list {
		if k.Alias == "b" {
			t.Fatalf("delete failed")
		}
	}
}

// --- SQLite-backed test -----------------------------------------------------

func TestSQLiteLLMKeyRepo_RoundTrip(t *testing.T) {
	// SQLite-backed repo: open an in-memory DB, run the same migration as
	// the production bootstrap (only the llm_keys table matters here).
	dbPath := t.TempDir() + "/llm.db"
	sealer := newTestSealer(t)
	db, err := openTestSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewSQLiteLLMKeyRepo(db, sealer)
	ctx := context.Background()
	k := llmkey.Key{UserID: "u1", Alias: "p", Provider: "openai", APIKey: "sk-abc", APIBase: "https://x", Enabled: true}
	if err := repo.Save(ctx, k); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "u1", "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "sk-abc" || got.APIBase != "https://x" {
		t.Fatalf("sqlite roundtrip mismatch: %+v", got)
	}
	// Verify the persisted row is ciphertext.
	var ct string
	if err := db.QueryRow(`SELECT api_key_kms FROM llm_keys WHERE user_id=? AND alias=?`, "u1", "p").Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct, "kms:v1:") {
		t.Fatalf("persisted column not KMS-shaped: %q", ct)
	}
	if strings.Contains(ct, "sk-abc") {
		t.Fatalf("api key leaked into SQLite row: %q", ct)
	}
}

// openTestSQLite creates a file-backed test database and runs the full
// production migration. Tests use a temp dir so the user-visible SQLite
// instance in ./data/code-agent.db is never touched.
func openTestSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS llm_keys (
  user_id TEXT NOT NULL,
  alias TEXT NOT NULL,
  provider TEXT NOT NULL,
  api_key_kms TEXT NOT NULL,
  api_base_kms TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, alias)
)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}