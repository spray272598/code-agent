package tool

import (
	"fmt"
	"strings"
)

// ValidateArgs checks tool args against a minimal JSON-schema subset
// (type=object, required[], properties with type string|number|integer|boolean|array|object).
func ValidateArgs(schema map[string]any, args map[string]any) error {
	if schema == nil {
		return nil
	}
	if args == nil {
		args = map[string]any{}
	}
	// required
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			name, _ := r.(string)
			if name == "" {
				continue
			}
			v, exists := args[name]
			if !exists || v == nil {
				return fmt.Errorf("missing required arg: %s", name)
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				return fmt.Errorf("required arg empty: %s", name)
			}
		}
	}
	// also support []string required
	if req, ok := schema["required"].([]string); ok {
		for _, name := range req {
			v, exists := args[name]
			if !exists || v == nil {
				return fmt.Errorf("missing required arg: %s", name)
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				return fmt.Errorf("required arg empty: %s", name)
			}
		}
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	for key, val := range args {
		ps, ok := props[key].(map[string]any)
		if !ok {
			continue // allow extra args
		}
		want, _ := ps["type"].(string)
		if want == "" || val == nil {
			continue
		}
		if !typeMatch(want, val) {
			return fmt.Errorf("arg %s: expected type %s, got %T", key, want, val)
		}
	}
	return nil
}

func typeMatch(want string, val any) bool {
	switch want {
	case "string":
		_, ok := val.(string)
		return ok
	case "number":
		switch val.(type) {
		case float64, float32, int, int64, int32:
			return true
		default:
			return false
		}
	case "integer":
		switch v := val.(type) {
		case int, int64, int32:
			return true
		case float64:
			return v == float64(int64(v))
		default:
			return false
		}
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "array":
		switch val.(type) {
		case []any, []string, []map[string]any:
			return true
		default:
			return false
		}
	case "object":
		_, ok := val.(map[string]any)
		return ok
	default:
		return true
	}
}

// IsReadOnly reports tools safe for parallel execution (walicode-style).
func IsReadOnly(name string) bool {
	base := name
	if i := strings.LastIndex(name, "__"); i >= 0 {
		base = name[i+2:]
	}
	switch strings.ToLower(base) {
	case "read_file", "glob", "grep", "memory_search", "list_files", "search",
		"web_search", "web_fetch", "echo", "time", "info", "stat", "get", "list", "find", "fetch":
		return true
	default:
		// heuristic: name contains read/list/search/get
		n := strings.ToLower(base)
		for _, k := range []string{"read", "list", "search", "find", "get", "fetch", "stat", "glob", "grep"} {
			if strings.Contains(n, k) {
				return true
			}
		}
		return false
	}
}

// IsCacheable read-only tools whose results can be short-TTL cached.
func IsCacheable(name string) bool {
	return IsReadOnly(name)
}
