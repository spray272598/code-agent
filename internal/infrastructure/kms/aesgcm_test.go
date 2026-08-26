package kms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newSealerInDir isolates the test's keyfile to a temp dir so the production
// ./secrets/kms.key is never touched. It also clears the env vars used by
// loadOrCreateKey so the keyfile path is the only source.
func newSealerInDir(t *testing.T) *Sealer {
	t.Helper()
	dir := t.TempDir()
	prev := EnvDir
	EnvDir = func() string { return dir }
	t.Cleanup(func() { EnvDir = prev })
	t.Setenv("CODE_AGENT_KMS_KEY", "")
	t.Setenv("CODE_AGENT_KMS_PREVIOUS", "")
	return mustSealer(t)
}

func mustSealer(t *testing.T) *Sealer {
	t.Helper()
	s, err := NewSealer()
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newSealerInDir(t)
	ctx := context.Background()

	plaintexts := [][]byte{
		[]byte("hunter2"),
		[]byte(strings.Repeat("very long ssh private key material... ", 64)),
		[]byte("sk-llm-api-key-must-stay-encrypted"),
	}
	for _, p := range plaintexts {
		ct, err := s.Encrypt(ctx, p)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if ct.KeyID != s.KeyID() {
			t.Fatalf("ciphertext key id mismatch")
		}
		got, err := s.Decrypt(ctx, ct)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if !bytes.Equal(got, p) {
			t.Fatalf("roundtrip mismatch")
		}
	}
}

func TestNonceIsFresh(t *testing.T) {
	s := newSealerInDir(t)
	ctx := context.Background()
	a, _ := s.Encrypt(ctx, []byte("x"))
	b, _ := s.Encrypt(ctx, []byte("x"))
	if bytes.Equal(a.Nonce, b.Nonce) {
		t.Fatalf("two encrypts of the same plaintext MUST produce different nonces")
	}
}

func TestTamperingDetected(t *testing.T) {
	s := newSealerInDir(t)
	ctx := context.Background()
	ct, err := s.Encrypt(ctx, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// flip a byte in the body
	ct.Body[0] ^= 0x01
	if _, err := s.Decrypt(ctx, ct); err == nil {
		t.Fatalf("decrypt must fail on tampered body")
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	prev := EnvDir
	EnvDir = func() string { return dir }
	t.Cleanup(func() { EnvDir = prev })

	// 1. Build a sealer with a fresh primary key; encrypt some data.
	s1, err := NewSealer()
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("round-1 secret")
	ct, err := s1.Encrypt(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	oldKeyID := s1.KeyID()
	oldHex := readActiveKeyfileHex(t, dir)

	// 2. Rotate: move the current primary hex into PREVIOUS, generate a fresh
	//    primary by deleting the keyfile and re-running with a new env.
	newHex := generateHex(t)
	t.Setenv("CODE_AGENT_KMS_PREVIOUS", oldHex)
	t.Setenv("CODE_AGENT_KMS_KEY", newHex)
	s2, err := NewSealer()
	if err != nil {
		t.Fatal(err)
	}
	if s2.KeyID() == oldKeyID {
		t.Fatalf("expected new primary key id, got same %q", oldKeyID)
	}

	// 3. Old ciphertext MUST still decrypt via the previous slot.
	got, err := s2.Decrypt(context.Background(), ct)
	if err != nil {
		t.Fatalf("rotated sealer failed to open old ciphertext: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("rotation plaintext mismatch")
	}

	// 4. New ciphertexts use the new primary id.
	ct2, err := s2.Encrypt(context.Background(), []byte("round-2"))
	if err != nil {
		t.Fatal(err)
	}
	if ct2.KeyID == oldKeyID {
		t.Fatalf("new ciphertext must use new key id, got old %q", oldKeyID)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	s := newSealerInDir(t)
	ct, err := s.Encrypt(context.Background(), []byte("persist-me"))
	if err != nil {
		t.Fatal(err)
	}
	s1 := Encode(ct)
	got, err := Decode(s1)
	if err != nil {
		t.Fatal(err)
	}
	if ct.KeyID != got.KeyID || !bytes.Equal(ct.Nonce, got.Nonce) || !bytes.Equal(ct.Body, got.Body) {
		t.Fatalf("encode/decode mismatch: in %+v out %+v", ct, got)
	}
	// Round-trip decrypt must still work.
	plain, err := s.Decrypt(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "persist-me" {
		t.Fatalf("plain mismatch")
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	cases := []string{
		"",
		"not-a-cipher",
		"v0:kid:abc",
		"v2:kid:abc",
		"v1:no-colon",
		"v1:kid:!!!notbase64!!!",
	}
	for _, c := range cases {
		if _, err := Decode(c); err == nil {
			t.Fatalf("Decode(%q) must fail", c)
		}
	}
}

func TestKeyfileCreatedWithRestrictivePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix file modes; keyfile ACLs are OS-specific")
	}
	dir := t.TempDir()
	prev := EnvDir
	EnvDir = func() string { return dir }
	t.Cleanup(func() { EnvDir = prev })
	t.Setenv("CODE_AGENT_KMS_KEY", "")
	t.Setenv("CODE_AGENT_KMS_PREVIOUS", "")

	if _, err := NewSealer(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, keyFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("keyfile missing: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Fatalf("keyfile perm = %o, want 0600", mode)
	}
	if dirInfo, err := os.Stat(dir); err == nil {
		if pm := dirInfo.Mode().Perm(); pm != 0o700 {
			t.Fatalf("keyfile dir perm = %o, want 0700", pm)
		}
	}
}

// helpers ------------------------------------------------------------------

func readActiveKeyfileHex(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func generateHex(t *testing.T) string {
	t.Helper()
	raw := make([]byte, keyBytes)
	for i := range raw {
		raw[i] = byte(i)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
