package redisx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
)

type Client struct {
	rdb     *redis.Client
	enabled bool
	// memory fallback rate limit
	mu      sync.Mutex
	hits    map[string][]time.Time
	lastGC  time.Time
	maxKeys int
}

func New(cfg config.RedisConfig) *Client {
	c := &Client{hits: map[string][]time.Time{}, maxKeys: 10000}
	if !cfg.Enabled {
		return c
	}
	c.rdb = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		fmt.Printf("[redis] unavailable (%v), fallback memory rate-limiter\n", err)
		if cerr := c.rdb.Close(); cerr != nil {
			fmt.Printf("[redis] close after failed ping: %v\n", cerr)
		}
		c.rdb = nil
		return c
	}
	c.enabled = true
	return c
}

func (c *Client) Enabled() bool { return c != nil && c.enabled }

func (c *Client) Close() error {
	if c != nil && c.rdb != nil {
		return c.rdb.Close()
	}
	return nil
}

func (c *Client) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	if !c.Enabled() {
		return "", redis.Nil
	}
	return c.rdb.Get(ctx, key).Result()
}

// TryLock acquires a distributed lock (SET NX). Returns true on success.
// When Redis is disabled it returns true so single-instance runs are unaffected.
func (c *Client) TryLock(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	if !c.Enabled() {
		return true, nil
	}
	return c.rdb.SetNX(ctx, key, val, ttl).Result()
}

// Unlock releases a distributed lock only if we still hold it (compare-and-delete),
// preventing one instance from unlatching a lock owned by another.
func (c *Client) Unlock(ctx context.Context, key, val string) error {
	if !c.Enabled() {
		return nil
	}
	const lua = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
	return c.rdb.Eval(ctx, lua, []string{key}, val).Err()
}

func (c *Client) IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error) {
	if !c.Enabled() {
		return 0, nil
	}
	pipe := c.rdb.Pipeline()
	incr := pipe.IncrBy(ctx, key, n)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// AllowRate sliding window per minute.
func (c *Client) AllowRate(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	if !c.Enabled() {
		return c.memoryAllow(key, limit, window), nil
	}
	now := time.Now().UnixMilli()
	minScore := now - window.Milliseconds()
	pipe := c.rdb.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", minScore))
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})
	cnt := pipe.ZCard(ctx, key)
	pipe.Expire(ctx, key, window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return c.memoryAllow(key, limit, window), nil
	}
	return cnt.Val() <= int64(limit), nil
}

func (c *Client) memoryAllow(key string, limit int, window time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	// periodic GC of stale keys (at most every 30s)
	if now.Sub(c.lastGC) > 30*time.Second {
		c.gcHitsLocked(now, window)
		c.lastGC = now
	}
	cut := now.Add(-window)
	arr := c.hits[key]
	var kept []time.Time
	for _, t := range arr {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		// free empty entry
		delete(c.hits, key)
	}
	if len(kept) >= limit {
		if len(kept) > 0 {
			c.hits[key] = kept
		}
		return false
	}
	c.hits[key] = append(kept, now)
	// hard cap map size
	if len(c.hits) > c.maxKeys {
		c.gcHitsLocked(now, window)
		// if still over, drop arbitrary oldest keys
		for k := range c.hits {
			if len(c.hits) <= c.maxKeys {
				break
			}
			if k != key {
				delete(c.hits, k)
			}
		}
	}
	return true
}

func (c *Client) gcHitsLocked(now time.Time, window time.Duration) {
	cut := now.Add(-window)
	if window <= 0 {
		cut = now.Add(-time.Minute)
	}
	for k, arr := range c.hits {
		var kept []time.Time
		for _, t := range arr {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(c.hits, k)
		} else {
			c.hits[k] = kept
		}
	}
}

// MemoryKeyCount for tests / metrics.
func (c *Client) MemoryKeyCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.hits)
}
