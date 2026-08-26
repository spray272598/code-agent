package main

import (
	"context"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	"github.com/spray272598/code-agent/internal/domain/ssh/port"
)

// fakeRunner captures the last command and returns canned output.
type fakeRunner struct {
	last string
	out  string
	code int
}

func (f *fakeRunner) Run(_ context.Context, command string) (string, int, error) {
	f.last = command
	return f.out, f.code, nil
}

// memRepo is a minimal in-memory IConnectionRepository for TUI tests.
type memRepo struct {
	byID   map[string]*model.ConnectionConfig
	byName map[string]*model.ConnectionConfig
}

func newMemRepo() *memRepo {
	return &memRepo{byID: map[string]*model.ConnectionConfig{}, byName: map[string]*model.ConnectionConfig{}}
}

func (m *memRepo) Save(_ context.Context, cfg *model.ConnectionConfig) error {
	if cfg.ID == "" {
		cfg.ID = "id-" + cfg.Name
	}
	cp := *cfg
	m.byID[cp.ID] = &cp
	m.byName[cp.Name] = &cp
	return nil
}

func (m *memRepo) FindByID(_ context.Context, id string) (*model.ConnectionConfig, error) {
	return m.byID[id], nil
}

func (m *memRepo) FindByName(_ context.Context, name string) (*model.ConnectionConfig, error) {
	return m.byName[name], nil
}

func (m *memRepo) List(_ context.Context) ([]*model.ConnectionConfig, error) {
	out := make([]*model.ConnectionConfig, 0, len(m.byName))
	for _, c := range m.byName {
		out = append(out, c)
	}
	return out, nil
}

func (m *memRepo) Delete(_ context.Context, id string) error {
	if c, ok := m.byID[id]; ok {
		delete(m.byName, c.Name)
		delete(m.byID, id)
	}
	return nil
}

func TestExecute_LocalCommand(t *testing.T) {
	runner := &fakeRunner{out: "hello\n", code: 0}
	app := NewApp(runner, nil, nil, nil)
	out, exit := app.Execute(context.Background(), "echo hello")
	if exit {
		t.Fatal("local command should not exit")
	}
	if runner.last != "echo hello" {
		t.Fatalf("runner got %q", runner.last)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("output = %q", out)
	}
}

func TestExecute_SlashExit(t *testing.T) {
	app := NewApp(nil, nil, nil, nil)
	_, exit := app.Execute(context.Background(), "/exit")
	if !exit {
		t.Fatal("expected exit=true")
	}
}

func TestExecute_SlashHelp(t *testing.T) {
	app := NewApp(nil, nil, nil, nil)
	out, _ := app.Execute(context.Background(), "/help")
	if !strings.Contains(out, "/conn") || !strings.Contains(out, "/ssh") {
		t.Fatalf("help missing commands: %q", out)
	}
}

func TestConn_AddListRemove(t *testing.T) {
	repo := newMemRepo()
	app := NewApp(nil, repo, nil, nil)

	out, _ := app.Execute(context.Background(), "/conn add web1 alice@10.0.0.1:2222 password s3cret")
	if !strings.Contains(out, "saved") {
		t.Fatalf("add: %q", out)
	}
	// Credential should be retrievable (not encrypted in this plain repo test).
	cfg, err := repo.FindByName(context.Background(), "web1")
	if err != nil || cfg == nil {
		t.Fatal("web1 not saved")
	}
	if cfg.Password != "s3cret" {
		t.Fatalf("password not stored: %q", cfg.Password)
	}
	if cfg.Port != 2222 {
		t.Fatalf("port parse wrong: %d", cfg.Port)
	}

	out, _ = app.Execute(context.Background(), "/conn list")
	if !strings.Contains(out, "web1") {
		t.Fatalf("list: %q", out)
	}

	out, _ = app.Execute(context.Background(), "/conn rm web1")
	if !strings.Contains(out, "revoked") {
		t.Fatalf("rm: %q", out)
	}
	if c, _ := repo.FindByName(context.Background(), "web1"); c != nil {
		t.Fatal("web1 should be gone")
	}
}

func TestConn_Add_KeyAuth(t *testing.T) {
	repo := newMemRepo()
	app := NewApp(nil, repo, nil, nil)
	out, _ := app.Execute(context.Background(), "/conn add db1 bob@db.host key /keys/db.pem")
	if !strings.Contains(out, "saved") {
		t.Fatalf("add key: %q", out)
	}
	cfg, _ := repo.FindByName(context.Background(), "db1")
	if cfg == nil || cfg.AuthType != "private_key" || cfg.PrivateKey != "/keys/db.pem" {
		t.Fatalf("key auth not stored: %+v", cfg)
	}
}

func TestConn_Add_MissingArgs(t *testing.T) {
	repo := newMemRepo()
	app := NewApp(nil, repo, nil, nil)
	out, _ := app.Execute(context.Background(), "/conn add")
	if !strings.Contains(out, "usage") {
		t.Fatalf("expected usage, got %q", out)
	}
}

func TestExecute_UnknownSlash(t *testing.T) {
	app := NewApp(nil, nil, nil, nil)
	out, _ := app.Execute(context.Background(), "/nope")
	if !strings.Contains(out, "unknown command") {
		t.Fatalf("got %q", out)
	}
}

// Compile-time check that memRepo satisfies the interface (silences unused import).
var _ port.IConnectionRepository = (*memRepo)(nil)
