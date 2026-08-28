package worktree

import (
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"abc-123_XyZ":        "abc-123_XyZ",
		"Foo/Bar:baz qux!@#": "FooBarbazqux",
		"../escape/attempt":  "escapeattempt",
		"":                   "",
		"---___":             "---___",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Fatalf("sanitize(%q)=%q want %q", in, got, want)
		}
	}
	// over-long ids are truncated to 40 runes
	long := strings.Repeat("a", 50) + strings.Repeat("b", 50)
	if got := sanitize(long); len(got) != 40 {
		t.Fatalf("sanitize truncation len=%d want 40", len(got))
	}
}
