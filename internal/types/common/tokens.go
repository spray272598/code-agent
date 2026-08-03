package common

import "unicode/utf8"

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
