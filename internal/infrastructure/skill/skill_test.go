package skillmarket

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateKeyPairAndSign(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("pub size = %d want %d", len(pub), ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Fatalf("priv size = %d want %d", len(priv), ed25519.PrivateKeySize)
	}

	md := "name: demo\ndescription: a skill"
	sigHex := SignSkill(md, priv)
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatalf("sig not hex: %v", err)
	}
	if !ed25519.Verify(pub, []byte(md), sig) {
		t.Fatal("signature should verify against public key")
	}
}

func TestSignSkillTamperFails(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	good := SignSkill("legit content", priv)
	// different content must produce a signature that does NOT verify for the original
	if ed25519.Verify(pub, []byte("legit content"), mustHexDecode(t, SignSkill("other content", priv))) {
		t.Fatal("different content should not verify as original")
	}
	// tampered original must not verify
	if ed25519.Verify(pub, []byte("legit content tampered"), mustHexDecode(t, good)) {
		t.Fatal("tampered content should fail verification")
	}
}

func TestLoadEd25519PublicKey(t *testing.T) {
	pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pub.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadEd25519PublicKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !sameKey(got, pub) {
		t.Fatal("loaded key differs from generated")
	}
	// invalid content -> error
	bad := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(bad, []byte("not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEd25519PublicKey(bad); err == nil {
		t.Fatal("expected error for invalid key file")
	}
}

func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	return b
}

func sameKey(a, b ed25519.PublicKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
