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
	mu   sync.Mutex
	hits map[string][]time.Time
}

func New(cfg config.RedisConfig) *Client {
	c := &Client{hits: map[string][]time.Time{}}
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
		fmt.Printf("[redis] unavailable (%v), fallback memory\n", err)
		_ = c.rdb.Close()
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
	cut := now.Add(-window)
	arr := c.hits[key]
	var kept []time.Time
	for _, t := range arr {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		c.hits[key] = kept
		return false
	}
	c.hits[key] = append(kept, now)
	return true
}
