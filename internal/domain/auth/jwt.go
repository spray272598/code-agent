package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Claims is the JWT payload for platform tokens.
type Claims struct {
	Sub   string // user id
	DID   string // device id (RFC8628)
	Role  string
	Email string
	Scope string
	JTI   string // refresh token id this access token is bound to
	Exp   int64  // expiry (unix seconds)
	Iat   int64  // issued at (unix seconds)
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

func b64(b []byte) string             { return base64.RawURLEncoding.EncodeToString(b) }
func b64Dec(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func signHS256(signed string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signed))
	return b64(mac.Sum(nil))
}

// Sign issues an HS256 JWT for the given claims using the provided secret.
func Sign(c Claims, secret []byte) (string, error) {
	if c.Exp == 0 {
		return "", errors.New("exp required")
	}
	if c.Iat == 0 {
		c.Iat = time.Now().Unix()
	}
	hdr, _ := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signed := b64(hdr) + "." + b64(payload)
	return signed + "." + signHS256(signed, secret), nil
}

// Parse validates the token signature against any of the provided secrets
// (enables zero-downtime rotation) and returns the claims. It rejects expired
// or malformed tokens.
func Parse(token string, secrets ...[]byte) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	signed := parts[0] + "." + parts[1]
	ok := false
	for _, s := range secrets {
		if s == nil {
			continue
		}
		expected := signHS256(signed, s)
		// Constant-time comparison prevents timing side-channel attacks.
		if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) == 1 {
			ok = true
			break
		}
	}
	if !ok {
		return nil, errors.New("invalid signature")
	}
	payload, err := b64Dec(parts[1])
	if err != nil {
		return nil, errors.New("malformed payload")
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, errors.New("malformed claims")
	}
	if c.Exp > 0 && time.Now().Unix() > c.Exp {
		return nil, errors.New("token expired")
	}
	return &c, nil
}
