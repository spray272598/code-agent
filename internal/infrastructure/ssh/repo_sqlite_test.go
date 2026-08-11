package ssh

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 创建 ssh_connection 表（与 internal/infrastructure/sqlite/db.go 中一致）
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS ssh_connection (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 22,
  username TEXT NOT NULL,
  auth_type TEXT NOT NULL DEFAULT 'password',
  password TEXT NOT NULL DEFAULT '',
  private_key TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  last_connected_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func TestSQLiteConnRepo_SaveAndFind(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	cfg := &model.ConnectionConfig{
		ID:       "test-1",
		Name:     "server1",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		AuthType: "password",
		Password: "secret",
		Enabled:  true,
	}
	if err := repo.Save(ctx, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	found, err := repo.FindByID(ctx, "test-1")
	if err != nil {
		t.Fatalf("findbyid: %v", err)
	}
	if found == nil {
		t.Fatal("expected found, got nil")
	}
	if found.Name != "server1" || found.Host != "192.168.1.1" {
		t.Fatalf("unexpected: %+v", found)
	}
	if !found.Enabled {
		t.Fatal("expected enabled=true")
	}
}

func TestSQLiteConnRepo_FindByName_NotFound(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	found, err := repo.FindByName(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("findbyname: %v", err)
	}
	if found != nil {
		t.Fatal("expected nil for nonexistent")
	}
}

func TestSQLiteConnRepo_FindByID_NotFound(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	found, err := repo.FindByID(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("findbyid: %v", err)
	}
	if found != nil {
		t.Fatal("expected nil for nonexistent")
	}
}

func TestSQLiteConnRepo_List(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	// 初始为空
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}

	// 添加两条
	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "1", Name: "a", Host: "h1", Port: 22, Username: "u"}); err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "2", Name: "b", Host: "h2", Port: 22, Username: "u"}); err != nil {
		t.Fatalf("save2: %v", err)
	}

	list, _ = repo.List(ctx)
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
}

func TestSQLiteConnRepo_Delete(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "1", Name: "a", Host: "h", Port: 22, Username: "u"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := repo.Delete(ctx, "1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	found, _ := repo.FindByID(ctx, "1")
	if found != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSQLiteConnRepo_Save_Upsert(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "1", Name: "a", Host: "h1", Port: 22, Username: "u"}); err != nil {
		t.Fatalf("save1: %v", err)
	}
	// upsert: same ID, different name/host/port
	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "1", Name: "updated", Host: "h2", Port: 2222, Username: "root"}); err != nil {
		t.Fatalf("save2 upsert: %v", err)
	}

	found, _ := repo.FindByID(ctx, "1")
	if found == nil {
		t.Fatal("expected found after upsert")
	}
	if found.Name != "updated" || found.Port != 2222 || found.Host != "h2" {
		t.Fatalf("upsert failed: %+v", found)
	}
}

func TestSQLiteConnRepo_FindByName(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	repo := NewSQLiteConnRepo(db)
	ctx := context.Background()

	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "1", Name: "myhost", Host: "10.0.0.1", Port: 2222, Username: "admin"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	found, err := repo.FindByName(ctx, "myhost")
	if err != nil {
		t.Fatalf("findbyname: %v", err)
	}
	if found == nil {
		t.Fatal("expected found")
	}
	if found.ID != "1" || found.Host != "10.0.0.1" {
		t.Fatalf("unexpected: %+v", found)
	}
}
