package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

// ToolCache short-TTL cache for identical MCP tool calls. Prevents duplicate
// invocations when the Agent loop retries the same tool.
type ToolCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cacheEntry
	max     int
}

type cacheEntry struct {
	text    string
	err     string
	expires time.Time
}

func NewToolCache(ttl time.Duration, max int) *ToolCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if max <= 0 {
		max = 256
	}
	return &ToolCache{ttl: ttl, max: max, entries: map[string]cacheEntry{}}
}

// Key computes a cache key from server/tool/args.
func (c *ToolCache) Key(serverName, toolName string, args map[string]any) string {
	b, _ := json.Marshal(args)
	sum := sha256.Sum256([]byte(serverName + "|" + toolName + "|"))
	sum = sha256.Sum256(append(sum[:], b...))
	return hex.EncodeToString(sum[:16])
}

// Get returns the cached result, if any.
func (c *ToolCache) Get(serverName, toolName string, args map[string]any) (string, bool) {
	if c == nil {
		return "", false
	}
	k := c.Key(serverName, toolName, args)
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

// Put stores a successful result. Errors are not cached.
func (c *ToolCache) Put(serverName, toolName string, args map[string]any, text string) {
	if c == nil || text == "" {
		return
	}
	k := c.Key(serverName, toolName, args)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
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

// Invalidate removes all cached entries related to a server.
func (c *ToolCache) Invalidate(serverName string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Best-effort: clear all entries since we don't track server granularity.
	// For a precise per-server invalidation the bridge should call this after
	// Sync(). In practice ToolCache is disabled when server state is unstable.
	c.entries = map[string]cacheEntry{}
}

// Clear empties the cache.
func (c *ToolCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}

// HealthSnapshot returns the current cached entry count (for observability).
func (c *ToolCache) HealthSnapshot() map[string]any {
	if c == nil {
		return map[string]any{"enabled": false}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"enabled": true,
		"entries": len(c.entries),
		"ttl_ms":  int(c.ttl / time.Millisecond),
		"max":     c.max,
	}
}

// Cacheable reports whether an MCP tool result is safe to cache. A tool is
// cacheable when it has an input schema with no write-side indicators and
// its description hints at read-only semantics.
func Cacheable(def model.ToolDef) bool {
	schema := def.InputSchema
	if schema == nil {
		return false
	}
	// Check for write-action keywords in input schema properties.
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	writeKeywords := map[string]bool{
		"write": true, "action": true, "execute": true, "create": true,
		"delete": true, "update": true, "modify": true, "add": true,
		"remove": true, "set": true, "put": true, "post": true,
	}
	for key := range props {
		if writeKeywords[strings.ToLower(key)] {
			return false
		}
	}
	// Also inspect description for write keywords as a secondary signal.
	desc := strings.ToLower(def.Description)
	for kw := range writeKeywords {
		if strings.Contains(desc, kw) {
			return false
		}
	}
	// Positive signal: description mentions "read", "list", "get", "query", "search"
	readKeywords := []string{"read", "list", "get", "query", "search", "find", "lookup", "show", "describe", "stat"}
	for _, kw := range readKeywords {
		if strings.Contains(desc, kw) {
			return true
		}
	}
	return len(props) > 0 // default: schemaed tools are probably safe
}
