package common

import (
	"fmt"
	"math"
	"unicode"
)

// EstimateTokens returns an approximate token count for s.
//
// It is language-aware rather than a flat "4 chars ≈ 1 token" heuristic:
//   - ASCII / Latin text:            ~4 chars per token  (weight 0.25)
//   - CJK (Han / Hiragana / Katakana / Hangul): ~1.5 tokens per character
//   - other Unicode (symbols, emoji): ~2 chars per token (weight 0.5/1.0)
//
// The flat divide-by-4 under-prices CJK by ~3-4x (Chinese is ~1-2 tokens per
// character), which risked overflowing the real model context window. This is
// still a heuristic — for exact counts prefer a real BPE tokenizer via the
// TokenCounter seam (see token_counter.go).
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	var t float64
	for _, r := range s {
		switch {
		case r <= 0x2000: // ASCII + Latin-1 + common punctuation
			t += 0.25
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			t += 1.5 // CJK: roughly 1-2 tokens per character
		case r > 0xFFFF: // astral planes: emoji, rare symbols
			t += 1.0
		default: // other BMP symbols / marks
			t += 0.5
		}
	}
	if t < 1 {
		return 1
	}
	return int(math.Ceil(t))
}

func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…[truncated]"
}

// TruncateRunesKeepTail bounds an overlong string while preserving BOTH the head
// and the tail. Unlike TruncateRunes (head-only), it does not drop the trailing
// content — for tool outputs / long messages the conclusion (e.g. the final error
// or answer) usually lives at the end, so a pure head truncation risks discarding
// exactly the most useful part. The middle is omitted behind an explicit marker.
//
// Budget split: 60% to the head (context / query / command), 40% to the tail
// (result / conclusion). The returned string is roughly `max` runes plus a small
// fixed-size marker.
func TruncateRunesKeepTail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	head := max * 3 / 5
	tail := max - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	if head+tail > max { // guard against rounding
		head = max / 2
		tail = max - head
	}
	omitted := len(r) - head - tail
	marker := fmt.Sprintf(" …[middle omitted: %d runes]… ", omitted)
	return string(r[:head]) + marker + string(r[len(r)-tail:])
}
