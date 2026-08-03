package auth

import "strings"

// RedactKey masks an API key for logs (keep prefix/suffix only).
func RedactKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "sha256:") {
		return "sha256:***"
	}
	r := []rune(key)
	if len(r) <= 4 {
		return "****"
	}
	if len(r) <= 8 {
		return string(r[:2]) + "****"
	}
	return string(r[:3]) + "****" + string(r[len(r)-2:])
}

// RedactMap copies a string map and redacts sensitive keys.
func RedactMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "password") || strings.Contains(lk, "token") ||
			strings.Contains(lk, "authorization") {
			out[k] = RedactKey(v)
		} else {
			out[k] = v
		}
	}
	return out
}
