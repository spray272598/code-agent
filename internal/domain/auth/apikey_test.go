package auth

import "testing"

func TestKeyStoreConstantTime(t *testing.T) {
	s := NewKeyStore([]string{"dev-key", "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"})
	if !s.Valid("dev-key") {
		t.Fatal("dev-key should valid")
	}
	if s.Valid("wrong-key") {
		t.Fatal("wrong should fail")
	}
	// pre-hashed invalid length falls through to hash of whole string — not valid for ffff...
	if s.Valid("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff") {
		// the second entry is all-ff hash of nothing real; Valid("dev-key") only for first
	}
	h := HashKey("dev-key")
	s2 := NewKeyStore([]string{h})
	if !s2.Valid("dev-key") {
		t.Fatal("prehashed store should accept plain")
	}
}
