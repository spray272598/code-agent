package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// ResultCache short-TTL cache for identical read-only tool calls.
type ResultCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cacheEntry
	max     int
}

type cacheEntry struct {
	text    string
	expires time.Time
}

func NewResultCache(ttl time.Duration, max int) *ResultCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if max <= 0 {
		max = 128
	}
	return &ResultCache{ttl: ttl, max: max, entries: map[string]cacheEntry{}}
}

func (c *ResultCache) Key(name string, args map[string]any) string {
	b, _ := json.Marshal(args)
	sum := sha256.Sum256(append([]byte(name+"|"), b...))
	return hex.EncodeToString(sum[:16])
}

func (c *ResultCache) Get(name string, args map[string]any) (string, bool) {
	if c == nil || !IsCacheable(name) {
		return "", false
	}
	k := c.Key(name, args)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.entries, k)
		}
		return "", false
	}
	return e.text, true
}

func (c *ResultCache) Put(name string, args map[string]any, text string) {
	if c == nil || !IsCacheable(name) || text == "" {
		return
	}
	// don't cache errors
	if len(text) >= 5 {
		low := text[:5]
		if low == "error" || low == "Error" {
			return
		}
	}
	k := c.Key(name, args)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		// drop arbitrary expired or first
		now := time.Now()
		for kk, e := range c.entries {
			if now.After(e.expires) {
				delete(c.entries, kk)
			}
		}
		if len(c.entries) >= c.max {
			for kk := range c.entries {
				delete(c.entries, kk)
				break
			}
		}
	}
	c.entries[k] = cacheEntry{text: text, expires: time.Now().Add(c.ttl)}
}
