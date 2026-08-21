package application

import (
	"context"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
)

func newTestDeviceStack(t *testing.T) (*DeviceService, *TokenService, string) {
	t.Helper()
	ctx := context.Background()
	users := repository.NewMemoryUserRepo()
	devices := repository.NewMemoryDeviceRepo()
	refresh := repository.NewMemoryRefreshTokenRepo()

	user := &auth.User{
		ID:            "usr_01",
		Email:         "a@b.com",
		DisplayName:   "A",
		Role:          "owner",
		Status:        auth.StatusActive,
		EmailVerified: true,
		PasswordHash:  "x",
		CreatedAt:     time.Now(),
	}
	if err := users.Save(ctx, user); err != nil {
		t.Fatal(err)
	}
	tok := NewTokenService(users, refresh, []byte("secret-1"), []byte("secret-0"))
	dev := NewDeviceService(devices, users, tok, "http://localhost:3000/verify", 5*time.Minute, 2*time.Millisecond)
	return dev, tok, user.ID
}

func newThrottledStack(t *testing.T) *DeviceService {
	t.Helper()
	users := repository.NewMemoryUserRepo()
	devices := repository.NewMemoryDeviceRepo()
	refresh := repository.NewMemoryRefreshTokenRepo()
	tok := NewTokenService(users, refresh, []byte("s"), nil)
	return NewDeviceService(devices, users, tok, "http://x/verify", 5*time.Minute, 10*time.Second)
}

func TestDeviceFlow(t *testing.T) {
	ctx := context.Background()
	dev, tok, userID := newTestDeviceStack(t)

	res, err := dev.StartAuthorization(ctx, DeviceAuthParams{Scope: "read"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.DeviceCode == "" || res.UserCode == "" {
		t.Fatalf("empty codes: %+v", res)
	}

	// poll before approval -> pending
	out, err := dev.Poll(ctx, res.DeviceCode)
	if err != nil {
		t.Fatalf("poll1: %v", err)
	}
	if out.Status != PollPending {
		t.Fatalf("expected pending, got %s (%s)", out.Status, out.OAuthError)
	}

	// approve
	if err := dev.Approve(ctx, res.UserCode, userID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// wait longer than the poll interval so the next poll isn't throttled
	time.Sleep(20 * time.Millisecond)

	// poll after approval -> approved with tokens
	out, err = dev.Poll(ctx, res.DeviceCode)
	if err != nil {
		t.Fatalf("poll2: %v", err)
	}
	if out.Status != PollApproved {
		t.Fatalf("expected approved, got %s", out.Status)
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", out)
	}

	// access token must validate and carry the device id (device_code)
	claims, err := tok.Validate(out.AccessToken)
	if err != nil {
		t.Fatalf("validate access: %v", err)
	}
	if claims.Sub != userID {
		t.Fatalf("access sub = %s, want %s", claims.Sub, userID)
	}
	if claims.DID != res.DeviceCode {
		t.Fatalf("access did = %s, want %s", claims.DID, res.DeviceCode)
	}

	// deny after approval should error
	if err := dev.Deny(ctx, res.UserCode); err != ErrDeviceAlreadyApproved {
		t.Fatalf("deny expected already-approved, got %v", err)
	}

	// unknown device_code
	out, err = dev.Poll(ctx, "nope")
	if err != nil {
		t.Fatalf("poll unknown: %v", err)
	}
	if out.Status != PollInvalid || out.OAuthError != "invalid_grant" {
		t.Fatalf("expected invalid_grant, got %s/%s", out.Status, out.OAuthError)
	}
}

func TestDeviceUserCodeFormat(t *testing.T) {
	c := formatUserCode("ABCDEFGH")
	if c != "ABCD-EFGH" {
		t.Fatalf("got %q", c)
	}
	if normalizeUserCode("ab-cd") != "ABCD" {
		t.Fatalf("normalize failed")
	}
}

func TestDeviceSlowDown(t *testing.T) {
	ctx := context.Background()
	dev := newThrottledStack(t)
	res, err := dev.StartAuthorization(ctx, DeviceAuthParams{})
	if err != nil {
		t.Fatal(err)
	}
	// first poll records the timestamp
	out, err := dev.Poll(ctx, res.DeviceCode)
	if err != nil || out.Status != PollPending {
		t.Fatalf("poll1 expected pending, got %s", out.Status)
	}
	// immediate re-poll within the interval must be throttled (slow_down)
	out, err = dev.Poll(ctx, res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PollPending || out.OAuthError != "slow_down" {
		t.Fatalf("expected slow_down, got %s/%s", out.Status, out.OAuthError)
	}
}
