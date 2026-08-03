package contextx

import (
	"context"
	"testing"
)

func TestCompressLevelsKeepsRecent(t *testing.T) {
	c := NewCompressor(100)
	c.KeepRecent = 3
	var hist []map[string]any
	for i := 0; i < 20; i++ {
		hist = append(hist, map[string]any{"role": "user", "content": "msg " + string(rune('a'+i%10)) + " xxxxxxxxxx"})
	}
	r := c.CompressLevels(context.Background(), hist, "", false)
	if len(r.History) < 3 {
		t.Fatalf("expected recent kept, got %d level=%s", len(r.History), r.Level)
	}
}
