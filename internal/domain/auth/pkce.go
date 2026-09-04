package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// ChallengeMethodS256 is the PKCE S256 code-challenge method (RFC 7636).
const ChallengeMethodS256 = "S256"

// GenerateCodeVerifier returns a high-entropy PKCE code verifier (43-char
// base64url string, 32 random bytes). See RFC 7636 §4.1.
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CodeChallenge derives the PKCE code challenge from a verifier. S256 applies
// base64url(sha256(verifier)); any other method is treated as "plain".
func CodeChallenge(verifier, method string) (string, error) {
	if method == ChallengeMethodS256 || method == "S256" {
		h := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(h[:]), nil
	}
	return verifier, nil
}
