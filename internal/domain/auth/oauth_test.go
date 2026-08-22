package auth

import (
	"testing"
	"time"
)

func TestGenerateCodeVerifier(t *testing.T) {
	v, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < CodeVerifierMinLen || len(v) > CodeVerifierMaxLen {
		t.Fatalf("verifier length out of range: %d", len(v))
	}
}

func TestValidatePKCE_S256(t *testing.T) {
	v, _ := GenerateCodeVerifier()
	ch, err := CodeChallenge(v, ChallengeMethodS256)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidatePKCE(v, ch, ChallengeMethodS256) {
		t.Fatal("valid S256 verifier should pass")
	}
	if ValidatePKCE("wrong", ch, ChallengeMethodS256) {
		t.Fatal("wrong verifier should fail")
	}
}

func TestValidatePKCE_Plain(t *testing.T) {
	v := "plain-verifier-1234567890abcdefghijklmnopqrstuvwxyz-0123456789"
	ch, _ := CodeChallenge(v, ChallengeMethodPlain)
	if !ValidatePKCE(v, ch, ChallengeMethodPlain) {
		t.Fatal("valid plain verifier should pass")
	}
}

func TestValidatePKCE_Length(t *testing.T) {
	if ValidatePKCE("short", "x", ChallengeMethodPlain) {
		t.Fatal("too-short verifier must fail")
	}
}

func TestAuthCodeStore_IssueRedeem(t *testing.T) {
	s := NewMemoryAuthCodeStore()
	rec := AuthCodeRecord{
		Code: "abc", UserID: "u1", Email: "a@b.c", Role: "user",
		RedirectURI: "https://app/cb", Challenge: "ch", ChallengeMeth: ChallengeMethodS256,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	if err := s.Issue(rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Redeem("abc")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("unexpected user %q", got.UserID)
	}
	// Second redeem → already used.
	if _, err := s.Redeem("abc"); err == nil {
		t.Fatal("expected already-used error")
	}
}

func TestAuthCodeStore_Expired(t *testing.T) {
	s := NewMemoryAuthCodeStore()
	_ = s.Issue(AuthCodeRecord{Code: "x", UserID: "u", ExpiresAt: time.Now().Add(-time.Minute)})
	if _, err := s.Redeem("x"); err == nil {
		t.Fatal("expired code should be rejected")
	}
}

func TestExchangeRedeem_Success(t *testing.T) {
	s := NewMemoryAuthCodeStore()
	v, _ := GenerateCodeVerifier()
	ch, _ := CodeChallenge(v, ChallengeMethodS256)
	_ = s.Issue(AuthCodeRecord{
		Code: "code1", UserID: "u1", Email: "a@b.c", Role: "user",
		RedirectURI: "https://app/cb", Challenge: ch, ChallengeMeth: ChallengeMethodS256,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	tok, err := ExchangeRedeem(s, ExchangeRequest{
		Code: "code1", ClientID: "cid", RedirectURI: "https://app/cb", CodeVerifier: v,
	}, []byte("secret"), time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.TokenType != "Bearer" {
		t.Fatalf("bad token: %#v", tok)
	}
	// The access token should be parsable by the platform JWT parser.
	if _, err := Parse(tok.AccessToken, []byte("secret")); err != nil {
		t.Fatalf("access token not valid JWT: %v", err)
	}
}

func TestExchangeRedeem_BadPKCE(t *testing.T) {
	s := NewMemoryAuthCodeStore()
	_ = s.Issue(AuthCodeRecord{
		Code: "code2", UserID: "u1", RedirectURI: "https://app/cb",
		Challenge: "ch", ChallengeMeth: ChallengeMethodS256, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if _, err := ExchangeRedeem(s, ExchangeRequest{
		Code: "code2", RedirectURI: "https://app/cb", CodeVerifier: "wrong-verifier-1234567890",
	}, []byte("secret"), time.Hour, time.Hour); err == nil {
		t.Fatal("wrong PKCE verifier should fail exchange")
	}
}

func TestExchangeRedeem_RedirectMismatch(t *testing.T) {
	s := NewMemoryAuthCodeStore()
	v, _ := GenerateCodeVerifier()
	ch, _ := CodeChallenge(v, ChallengeMethodS256)
	_ = s.Issue(AuthCodeRecord{
		Code: "code3", UserID: "u1", RedirectURI: "https://app/cb",
		Challenge: ch, ChallengeMeth: ChallengeMethodS256, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if _, err := ExchangeRedeem(s, ExchangeRequest{
		Code: "code3", RedirectURI: "https://evil/cb", CodeVerifier: v,
	}, []byte("secret"), time.Hour, time.Hour); err == nil {
		t.Fatal("redirect_uri mismatch should fail exchange")
	}
}
