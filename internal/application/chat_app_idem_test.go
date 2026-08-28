package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeIdem is an in-memory idemStore for unit-testing the idempotency wiring
// without a live Redis. It mirrors the SET-NX semantics of redisx.Client.
type fakeIdem struct {
	mu   sync.Mutex
	data map[string]string
}

func newFakeIdem() *fakeIdem { return &fakeIdem{data: map[string]string{}} }

func (f *fakeIdem) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[key], nil
}

func (f *fakeIdem) TryReserve(_ context.Context, key, val string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[key]; ok {
		return false, nil
	}
	f.data[key] = val
	return true, nil
}

func (f *fakeIdem) Set(_ context.Context, key, val string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = val
	return nil
}

func newIdemApp() (*ChatApp, *fakeIdem) {
	a := &ChatApp{}
	f := newFakeIdem()
	a.idemSvc = &IdempotencyService{idem: f}
	return a, f
}

func TestCheckIdempotencyNoKey(t *testing.T) {
	a, _ := newIdemApp()
	st, cached, err := a.checkIdempotency(context.Background(), ChatRequest{Message: "hi"})
	if st != "none" || cached != nil || err != nil {
		t.Fatalf("expected none, got st=%s cached=%v err=%v", st, cached, err)
	}
}

func TestCheckIdempotencyNilStore(t *testing.T) {
	a := &ChatApp{idemSvc: &IdempotencyService{}} // no idem, no redis → degrade to none
	st, _, _ := a.checkIdempotency(context.Background(), ChatRequest{IdempotencyKey: "k", UserID: "u"})
	if st != "none" {
		t.Fatalf("expected none for nil store, got %s", st)
	}
}

func TestCheckIdempotencyReserveThenPending(t *testing.T) {
	a, f := newIdemApp()
	req := ChatRequest{IdempotencyKey: "k", UserID: "u"}

	st, _, _ := a.checkIdempotency(context.Background(), req)
	if st != "none" {
		t.Fatalf("first call should be none, got %s", st)
	}
	if _, ok := f.data["idem:u:k"]; !ok {
		t.Fatal("first call should have reserved a pending slot")
	}

	st2, _, _ := a.checkIdempotency(context.Background(), req)
	if st2 != "pending" {
		t.Fatalf("second call should be pending (in-flight), got %s", st2)
	}
}

func TestCheckIdempotencyReplayDone(t *testing.T) {
	a, f := newIdemApp()
	resp := &ChatResponse{SessionID: "s1", Response: "hello", TokenUsed: 12}
	b, _ := json.Marshal(resp)
	f.data["idem:u:k"] = "done:" + string(b)

	st, cached, err := a.checkIdempotency(context.Background(), ChatRequest{IdempotencyKey: "k", UserID: "u"})
	if st != "done" || err != nil {
		t.Fatalf("expected done, got st=%s err=%v", st, err)
	}
	if cached == nil || cached.Response != "hello" || cached.TokenUsed != 12 {
		t.Fatalf("cached response mismatch: %+v", cached)
	}
}

func TestCheckIdempotencyReplayError(t *testing.T) {
	a, f := newIdemApp()
	f.data["idem:u:k"] = "err:boom"

	st, _, err := a.checkIdempotency(context.Background(), ChatRequest{IdempotencyKey: "k", UserID: "u"})
	if st != "error" {
		t.Fatalf("expected error status, got %s", st)
	}
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
}

func TestStoreIdempotency(t *testing.T) {
	a, f := newIdemApp()
	req := ChatRequest{IdempotencyKey: "k", UserID: "u"}

	a.storeIdempotency(context.Background(), req, &ChatResponse{Response: "ok"}, nil)
	if v := f.data["idem:u:k"]; !hasPrefix(v, "done:") {
		t.Fatalf("success should store done:, got %q", v)
	}

	a.storeIdempotency(context.Background(), req, nil, errors.New("fail"))
	if v := f.data["idem:u:k"]; !hasPrefix(v, "err:fail") {
		t.Fatalf("error should store err:fail, got %q", v)
	}

	// no key → no-op, must not touch the store
	a.storeIdempotency(context.Background(), ChatRequest{}, &ChatResponse{}, nil)
	if len(f.data) != 1 {
		t.Fatalf("no-key store should be a no-op, data=%v", f.data)
	}
}

func TestIdemKeyScoping(t *testing.T) {
	if got := idemKey("", "k"); got != "idem:k" {
		t.Fatalf("empty user should be idem:k, got %q", got)
	}
	if got := idemKey("u", "k"); got != "idem:u:k" {
		t.Fatalf("scoped key should be idem:u:k, got %q", got)
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
