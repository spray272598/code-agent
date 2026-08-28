package vector

import "testing"

func TestMatchPayload(t *testing.T) {
	payload := map[string]any{
		"scope": "project",
		"type":  "doc",
		"n":     3,
	}
	if !MatchPayload(payload, map[string]any{}) {
		t.Fatal("empty filter should match everything")
	}
	if !MatchPayload(payload, map[string]any{"scope": "project"}) {
		t.Fatal("matching key/value should pass")
	}
	if !MatchPayload(payload, map[string]any{"scope": "project", "type": "doc"}) {
		t.Fatal("multi-key match should pass")
	}
	if MatchPayload(payload, map[string]any{"scope": "user"}) {
		t.Fatal("wrong value should fail")
	}
	if MatchPayload(payload, map[string]any{"missing": 1}) {
		t.Fatal("missing key should fail")
	}
	if MatchPayload(payload, map[string]any{"n": 4}) {
		t.Fatal("numeric mismatch should fail")
	}
}
