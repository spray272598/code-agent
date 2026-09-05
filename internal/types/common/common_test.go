package common

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"single ascii", "a", 1},
		{"four ascii", "abcd", 1},
		{"hundred ascii", strings.Repeat("x", 100), 25},
		{"cjk heavier", "中文", 3}, // 2 CJK chars * 1.5 = 3
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EstimateTokens(c.in); got != c.want {
				t.Fatalf("EstimateTokens(%q)=%d want %d", c.in, got, c.want)
			}
		})
	}
	// any non-empty string yields at least 1 token
	if EstimateTokens("z") < 1 {
		t.Fatal("non-empty must be >=1")
	}
	// CJK priced higher than ASCII of equal length
	if EstimateTokens("中文中文") <= EstimateTokens("abcd") {
		t.Fatal("CJK should cost more tokens than ASCII of same rune count")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("abc", 0); got != "" {
		t.Fatalf("max<=0 -> %q", got)
	}
	if got := TruncateRunes("hello", 10); got != "hello" {
		t.Fatalf("short passthrough -> %q", got)
	}
	long := "hello world this is long"
	got := TruncateRunes(long, 5)
	if !strings.HasPrefix(got, "hel") || !strings.Contains(got, "[truncated]") {
		t.Fatalf("truncation malformed: %q", got)
	}
	if len([]rune(got)) > 5+len("…[truncated]") {
		t.Fatalf("exceeds budget: %q", got)
	}
}

func TestTruncateRunesKeepTail(t *testing.T) {
	if got := TruncateRunesKeepTail("abc", 0); got != "" {
		t.Fatalf("max<=0 -> %q", got)
	}
	short := "abcdef"
	if got := TruncateRunesKeepTail(short, 20); got != short {
		t.Fatalf("short passthrough -> %q", got)
	}
	long := strings.Repeat("H", 40) + strings.Repeat("T", 40) // 80 runes
	max := 20
	got := TruncateRunesKeepTail(long, max)
	if !strings.HasPrefix(got, "HHH") {
		t.Fatalf("head not preserved: %q", got)
	}
	if !strings.HasSuffix(got, "TTT") {
		t.Fatalf("tail not preserved: %q", got)
	}
	if !strings.Contains(got, "middle omitted") {
		t.Fatalf("missing omission marker: %q", got)
	}
	r := []rune(got)
	if len(r) > max+len(" …[middle omitted: 0000 runes]… ") {
		t.Fatalf("budget overrun: len=%d", len(r))
	}
}

func TestTruncateStr(t *testing.T) {
	if got := TruncateStr("abc", 0); got != "" {
		t.Fatalf("max<=0 -> %q", got)
	}
	if got := TruncateStr("hello", 10); got != "hello" {
		t.Fatalf("short passthrough -> %q", got)
	}
	if got := TruncateStr("abcdef", 3); got != "abc" {
		t.Fatalf("tiny max no ellipsis -> %q", got)
	}
	if got := TruncateStr("hello world", 5); got != "he..." {
		t.Fatalf("standard -> %q", got)
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 123: "123", -7: "-7", -100: "-100"}
	for n, want := range cases {
		if got := Itoa(n); got != want {
			t.Fatalf("Itoa(%d)=%q want %q", n, got, want)
		}
	}
}

func TestHeuristicCounter(t *testing.T) {
	c := HeuristicCounter{}
	if got := c.CountTokens("abcd"); got != 1 {
		t.Fatalf("HeuristicCounter.CountTokens= %d want 1", got)
	}
	// seam parity with EstimateTokens
	if c.CountTokens("hello world") != EstimateTokens("hello world") {
		t.Fatal("HeuristicCounter must equal EstimateTokens")
	}
}

func TestTiktokenCounter(t *testing.T) {
	c := NewTiktokenCounter("cl100k_base")

	// Empty string
	if got := c.CountTokens(""); got != 0 {
		t.Fatalf("TiktokenCounter empty: got %d, want 0", got)
	}

	// ASCII text - should return positive count
	got := c.CountTokens("hello world")
	if got <= 0 {
		t.Fatalf("TiktokenCounter ASCII: got %d, want > 0", got)
	}
	t.Logf("TiktokenCounter('hello world') = %d", got)

	// CJK text
	got = c.CountTokens("你好世界")
	if got <= 0 {
		t.Fatalf("TiktokenCounter CJK: got %d, want > 0", got)
	}
	t.Logf("TiktokenCounter('你好世界') = %d", got)

	// Longer text should have more tokens
	short := c.CountTokens("hi")
	long := c.CountTokens("hello world this is a longer sentence")
	if long <= short {
		t.Fatalf("longer text should have more tokens: short=%d long=%d", short, long)
	}

	// Default encoding
	c2 := NewTiktokenCounter("")
	got2 := c2.CountTokens("test")
	if got2 <= 0 {
		t.Fatalf("TiktokenCounter default encoding: got %d, want > 0", got2)
	}
}

func TestTiktokenCounterFallback(t *testing.T) {
	// Invalid encoding should fall back to heuristic
	c := NewTiktokenCounter("invalid_encoding_12345")
	got := c.CountTokens("hello")
	if got <= 0 {
		t.Fatalf("TiktokenCounter fallback: got %d, want > 0", got)
	}
}
