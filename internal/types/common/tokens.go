package common

import (
	"fmt"
	"unicode/utf8"
)

// EstimateTokens rough 4 chars ≈ 1 token heuristic (CJK-aware via runes).
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	t := n / 4
	if t < 1 {
		return 1
	}
	return t
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
