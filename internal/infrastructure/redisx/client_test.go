package redisx

import (
	"testing"
	"time"
)

func TestMemoryRateLimiterGC(t *testing.T) {
	c := &Client{hits: map[string][]time.Time{}, maxKeys: 100}
	old := time.Now().Add(-2 * time.Minute)
	c.hits["stale"] = []time.Time{old, old}
	c.hits["fresh"] = []time.Time{time.Now()}
	c.lastGC = time.Time{} // force GC on next allow
	ok := c.memoryAllow("fresh", 10, time.Minute)
	if !ok {
		t.Fatal("fresh should allow")
	}
	if _, exists := c.hits["stale"]; exists {
		t.Fatal("stale key should be GC'd")
	}
}
