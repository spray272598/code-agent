package ssh

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
)

// fakeRepo records operations so tests can assert the ciphertext path.
type fakeRepo struct {
	byID    map[string]*model.ConnectionConfig
	saveErr error
	listErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byID: map[string]*model.ConnectionConfig{}} }

func (r *fakeRepo) Save(_ context.Context, cfg *model.ConnectionConfig) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byID[cfg.ID] = cfg
	return nil
}
func (r *fakeRepo) FindByID(_ context.Context, id string) (*model.ConnectionConfig, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.byID[id], nil
}
func (r *fakeRepo) FindByName(_ context.Context, name string) (*model.ConnectionConfig, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	for _, c := range r.byID {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, nil
}
func (r *fakeRepo) List(_ context.Context) ([]*model.ConnectionConfig, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]*model.ConnectionConfig, 0, len(r.byID))
	for _, c := range rangeFake(r.byID) {
		out = append(out, c)
	}
	return out, nil
}
func (r *fakeRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

// rangeFake iterates the map deterministically by ID (insertion order is
// randomized; tests should not rely on it).
func rangeFake(m map[string]*model.ConnectionConfig) []*model.ConnectionConfig {
	out := make([]*model.ConnectionConfig, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

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

// kmsinfra aliases the package to disambiguate the domain port name vs the
// infra implementation.
var _ = kmsinfra.NewSealer

func TestEncryptingRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	sealer := newTestSealer(t)
	repo := NewEncryptingConnRepo(inner, sealer)

	plain := &model.ConnectionConfig{
		ID: "ssh_01", Name: "prod", Host: "10.0.0.1", Port: 22,
		Username: "ops", AuthType: "password", Password: "hunter2",
	}
	if err := repo.Save(ctx, plain); err != nil {
		t.Fatal(err)
	}

	// The inner repo must NOT contain the plaintext password.
	stored := inner.byID["ssh_01"]
	if strings.Contains(stored.Password, "hunter2") {
		t.Fatalf("password leaked to storage: %q", stored.Password)
	}
	if !strings.HasPrefix(stored.Password, kmsCiphertextPrefix) {
		t.Fatalf("password not KMS-prefixed: %q", stored.Password)
	}

	got, err := repo.FindByID(ctx, "ssh_01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "hunter2" {
		t.Fatalf("decrypted password mismatch: %q", got.Password)
	}
}

func TestEncryptingRepo_PrivateKeyField(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	repo := NewEncryptingConnRepo(inner, newTestSealer(t))
	keyMaterial := "-----BEGIN OPENSSH PRIVATE KEY-----\nfoo\nbar\n-----END OPENSSH PRIVATE KEY-----"
	cfg := &model.ConnectionConfig{
		ID: "ssh_02", Name: "bastion", Host: "10.0.0.2", Port: 22,
		Username: "ops", AuthType: "private_key", PrivateKey: keyMaterial,
	}
	if err := repo.Save(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inner.byID["ssh_02"].PrivateKey, "BEGIN OPENSSH") {
		t.Fatalf("private key leaked to storage")
	}
	got, err := repo.FindByID(ctx, "ssh_02")
	if err != nil {
		t.Fatal(err)
	}
	if got.PrivateKey != keyMaterial {
		t.Fatalf("decrypted private key mismatch")
	}
}

func TestEncryptingRepo_LegacyPlaintextPassesThrough(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	repo := NewEncryptingConnRepo(inner, newTestSealer(t))
	// Pre-populate with a legacy plaintext record (no KMS prefix).
	inner.byID["ssh_03"] = &model.ConnectionConfig{
		ID: "ssh_03", Name: "legacy", Host: "10.0.0.3", Port: 22,
		Username: "ops", AuthType: "password", Password: "legacy-cleartext",
	}
	got, err := repo.FindByID(ctx, "ssh_03")
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "legacy-cleartext" {
		t.Fatalf("legacy plaintext must pass through, got %q", got.Password)
	}
}

func TestEncryptingRepo_ListDecryptsAll(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	repo := NewEncryptingConnRepo(inner, newTestSealer(t))
	for i, pwd := range []string{"alpha", "bravo", "charlie"} {
		cfg := &model.ConnectionConfig{
			ID: "ssh_l" + string(rune('0'+i)), Name: "n" + string(rune('0'+i)),
			Host: "10.0.0.1", Port: 22, Username: "ops",
			AuthType: "password", Password: pwd,
		}
		if err := repo.Save(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Password == "" {
			t.Fatalf("list did not decrypt: id=%s", r.ID)
		}
		if strings.HasPrefix(r.Password, kmsCiphertextPrefix) {
			t.Fatalf("list leaked ciphertext: id=%s", r.ID)
		}
	}
}

func TestEncryptingRepo_FailClosedOnDecrypt(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	repo := NewEncryptingConnRepo(inner, newTestSealer(t))
	// Store a ciphertext produced by a different (simulated) sealer.
	inner.byID["ssh_x"] = &model.ConnectionConfig{
		ID: "ssh_x", Name: "x", Host: "10.0.0.1", Port: 22, Username: "ops",
		AuthType: "password", Password: kmsCiphertextPrefix + "v1:otherkid:AAAA",
	}
	// The decorator should refuse to silently leak — return an error from
	// FindByID so the caller knows the record is corrupt.
	if _, err := repo.FindByID(ctx, "ssh_x"); err == nil {
		t.Fatalf("expected decrypt failure, got nil")
	}
	if _, err := repo.List(ctx); err != nil {
		t.Fatalf("List returns nil error (it drops bad rows), got %v", err)
	}
}

func TestEncryptingRepo_DeleteDelegates(t *testing.T) {
	ctx := context.Background()
	inner := newFakeRepo()
	repo := NewEncryptingConnRepo(inner, newTestSealer(t))
	if err := repo.Save(ctx, &model.ConnectionConfig{ID: "ssh_d", Name: "d", Host: "10.0.0.1", Port: 22, Username: "ops", AuthType: "password", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, "ssh_d"); err != nil {
		t.Fatal(err)
	}
	if _, ok := inner.byID["ssh_d"]; ok {
		t.Fatalf("delete failed")
	}
}

func TestEncryptingRepo_PanicsOnNilDeps(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = NewEncryptingConnRepo(nil, nil)
}

// Sanity: the sshport import compiles end-to-end.
var _ sshport.IConnectionRepository = (*EncryptingConnRepo)(nil)

// Sanity: the kms import compiles end-to-end.
var _ = errors.Is