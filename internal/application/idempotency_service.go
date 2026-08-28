package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
)

type IdempotencyService struct {
	idem idemStore
}

type idemStore interface {
	Get(ctx context.Context, key string) (string, error)
	TryReserve(ctx context.Context, key, val string, ttl time.Duration) (bool, error)
	Set(ctx context.Context, key, val string, ttl time.Duration) error
}

const idemWindow = 10 * time.Minute

func idemKey(userID, key string) string {
	if userID == "" {
		return "idem:" + key
	}
	return "idem:" + userID + ":" + key
}

func (s *IdempotencyService) getIdemStore(redis *redisx.Client) idemStore {
	if s.idem != nil {
		return s.idem
	}
	if redis != nil && redis.Enabled() {
		return redis
	}
	return nil
}

func (s *IdempotencyService) checkIdempotency(ctx context.Context, req ChatRequest, redis *redisx.Client) (status string, cached *ChatResponse, err error) {
	if req.IdempotencyKey == "" {
		return "none", nil, nil
	}
	store := s.getIdemStore(redis)
	if store == nil {
		return "none", nil, nil
	}
	key := idemKey(req.UserID, req.IdempotencyKey)
	val, gerr := store.Get(ctx, key)
	if gerr != nil {
		return "none", nil, nil
	}
	if val == "" {
		ok, rerr := store.TryReserve(ctx, key, "pending", idemWindow)
		if rerr != nil {
			return "none", nil, nil
		}
		if !ok {
			return "pending", nil, nil
		}
		return "none", nil, nil
	}
	if val == "pending" {
		return "pending", nil, nil
	}
	if strings.HasPrefix(val, "done:") {
		var resp ChatResponse
		if jerr := json.Unmarshal([]byte(val[len("done:"):]), &resp); jerr == nil {
			return "done", &resp, nil
		}
		return "none", nil, nil
	}
	if strings.HasPrefix(val, "err:") {
		return "error", nil, errors.New(val[len("err:"):])
	}
	return "none", nil, nil
}

func (s *IdempotencyService) storeIdempotency(ctx context.Context, req ChatRequest, resp *ChatResponse, runErr error, redis *redisx.Client) {
	if req.IdempotencyKey == "" {
		return
	}
	store := s.getIdemStore(redis)
	if store == nil {
		return
	}
	key := idemKey(req.UserID, req.IdempotencyKey)
	if runErr != nil {
		_ = store.Set(ctx, key, "err:"+runErr.Error(), idemWindow)
		return
	}
	if resp != nil {
		if b, jerr := json.Marshal(resp); jerr == nil {
			_ = store.Set(ctx, key, "done:"+string(b), idemWindow)
		}
	}
}
