package common

import "unicode/utf8"

// TruncateStr truncates a string to maxLen runes, appending "..." if truncated.
// It is rune-safe and handles multi-byte characters correctly.
func TruncateStr(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string([]rune(s)[:maxLen])
	}
	buf := make([]rune, 0, maxLen)
	for i, r := range s {
		if i >= maxLen-3 {
			break
		}
		buf = append(buf, r)
	}
	return string(buf) + "..."
}

// Itoa converts an integer to its decimal string representation.
// It avoids importing strconv for hot paths.
func Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + Itoa(-n)
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
