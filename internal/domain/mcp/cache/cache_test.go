package mcp

import (
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

func TestNewToolCacheDefaults(t *testing.T) {
	c := NewToolCache(0, 0)
	snap := c.HealthSnapshot()
	if snap["enabled"] != true {
		t.Fatal("expected enabled")
	}
	if snap["max"] != 256 {
		t.Fatalf("default max = %v want 256", snap["max"])
	}
	if snap["ttl_ms"] != int((30*time.Second)/time.Millisecond) {
		t.Fatalf("default ttl = %v", snap["ttl_ms"])
	}
}

func TestToolCacheKeyDeterministic(t *testing.T) {
	c := NewToolCache(0, 0)
	k1 := c.Key("srv", "tool", map[string]any{"a": 1})
	k2 := c.Key("srv", "tool", map[string]any{"a": 1})
	if k1 != k2 {
		t.Fatalf("key not deterministic: %q vs %q", k1, k2)
	}
	k3 := c.Key("srv", "tool", map[string]any{"a": 2})
	if k1 == k3 {
		t.Fatal("different args must yield different key")
	}
	if c.Key("srvA", "tool", nil) == c.Key("srvB", "tool", nil) {
		t.Fatal("server name must affect key")
	}
}

func TestToolCachePutGet(t *testing.T) {
	c := NewToolCache(time.Minute, 0)
	if _, ok := c.Get("s", "t", map[string]any{"x": 1}); ok {
		t.Fatal("empty cache should miss")
	}
	c.Put("s", "t", map[string]any{"x": 1}, "result")
	got, ok := c.Get("s", "t", map[string]any{"x": 1})
	if !ok || got != "result" {
		t.Fatalf("expected hit got (%q,%v)", got, ok)
	}
	// empty text is not cached
	c.Put("s", "t2", map[string]any{}, "")
	if _, ok := c.Get("s", "t2", map[string]any{}); ok {
		t.Fatal("empty text must not be cached")
	}
}

func TestToolCacheInvalidateAndClear(t *testing.T) {
	c := NewToolCache(time.Minute, 0)
	c.Put("s", "t", map[string]any{}, "v")
	c.Invalidate("s")
	if _, ok := c.Get("s", "t", map[string]any{}); ok {
		t.Fatal("Invalidate should clear entries")
	}
	c.Put("s", "t", map[string]any{}, "v")
	c.Clear()
	if _, ok := c.Get("s", "t", map[string]any{}); ok {
		t.Fatal("Clear should empty cache")
	}
}

func TestCacheable(t *testing.T) {
	// nil schema -> not cacheable
	if Cacheable(model.ToolDef{}) {
		t.Fatal("nil schema must be non-cacheable")
	}
	// properties not a map -> not cacheable
	if Cacheable(model.ToolDef{InputSchema: map[string]any{"properties": "nope"}}) {
		t.Fatal("non-map properties must be non-cacheable")
	}
	// write keyword in property name -> not cacheable
	if Cacheable(model.ToolDef{
		InputSchema: map[string]any{"properties": map[string]any{"write": nil}},
	}) {
		t.Fatal("write property must disable caching")
	}
	// write keyword in description -> not cacheable
	if Cacheable(model.ToolDef{
		InputSchema: map[string]any{"properties": map[string]any{"path": nil}},
		Description: "execute a command",
	}) {
		t.Fatal("write-verb description must disable caching")
	}
	// read-only description with schema -> cacheable
	if !Cacheable(model.ToolDef{
		InputSchema: map[string]any{"properties": map[string]any{"path": nil}},
		Description: "get file content",
	}) {
		t.Fatal("read-only schemaed tool should be cacheable")
	}
	// schemaed but no read keyword -> still cacheable (default allow)
	if !Cacheable(model.ToolDef{
		InputSchema: map[string]any{"properties": map[string]any{"query": nil}},
		Description: "lookup records",
	}) {
		t.Fatal("schemaed tool without write signals should be cacheable")
	}
}
