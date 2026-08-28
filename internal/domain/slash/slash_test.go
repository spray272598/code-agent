package slash

import (
	"strings"
	"testing"
)

func TestRegistryBuiltins(t *testing.T) {
	r := NewRegistry()
	ctx := Context{}

	// non-slash passthrough
	if res := r.Try("hello world", ctx); res.Handled || res.Rewrite != "" {
		t.Fatalf("plain text should be unhandled: %+v", res)
	}

	// bare "/" falls back to help
	res := r.Try("/", ctx)
	if !res.Handled || !strings.Contains(res.Response, "Slash commands") {
		t.Fatalf("/ -> %+v", res)
	}

	// /help lists all builtins
	res = r.Try("/help", ctx)
	if !res.Handled || !strings.Contains(res.Response, "/compact") || !strings.Contains(res.Response, "/tools") {
		t.Fatalf("/help -> %+v", res)
	}

	// /compact requests compression
	res = r.Try("/compact", ctx)
	if !res.Handled || !res.ForceCompact {
		t.Fatalf("/compact -> %+v", res)
	}

	// /cost, /memory, /index, /teams are informational and handled
	for _, cmd := range []string{"/cost", "/memory", "/index", "/teams"} {
		if res := r.Try(cmd, ctx); !res.Handled {
			t.Fatalf("%s should be handled", cmd)
		}
	}
}

func TestRegistryTools(t *testing.T) {
	r := NewRegistry()
	ctx := Context{
		ListTools: func() []map[string]string {
			return []map[string]string{
				{"name": "bash", "description": "run shell"},
				{"name": "read_file", "description": "read a file"},
			}
		},
	}
	res := r.Try("/tools", ctx)
	if !res.Handled || !strings.Contains(res.Response, "2 tools:") ||
		!strings.Contains(res.Response, "bash") || !strings.Contains(res.Response, "read_file") {
		t.Fatalf("/tools -> %+v", res)
	}

	// missing helper -> graceful
	res = r.Try("/tools", Context{})
	if !res.Handled || !strings.Contains(res.Response, "unavailable") {
		t.Fatalf("/tools nil -> %+v", res)
	}
}

func TestRegistrySkillRewrite(t *testing.T) {
	r := NewRegistry()
	// /skill <id> with no extra message
	res := r.Try("/skill foo", Context{})
	if res.Handled {
		t.Fatalf("/skill should NOT be locally handled: %+v", res)
	}
	if res.Rewrite != "使用 skill foo Execute skill foo" {
		t.Fatalf("/skill rewrite = %q", res.Rewrite)
	}
	// /skill with extra message
	res = r.Try("/skill foo do the thing", Context{})
	if res.Rewrite != "使用 skill foo do the thing" {
		t.Fatalf("/skill msg rewrite = %q", res.Rewrite)
	}
	// /skill with no id -> usage
	res = r.Try("/skill", Context{})
	if !res.Handled || !strings.Contains(res.Response, "usage") {
		t.Fatalf("/skill no-id -> %+v", res)
	}
}

func TestRegistryPassThroughPrefixes(t *testing.T) {
	r := NewRegistry()
	// /team, /parallel, /deep are routed to the agent (not local)
	for _, cmd := range []string{"/team build a feature", "/parallel x", "/deep y"} {
		res := r.Try(cmd, Context{})
		if res.Handled {
			t.Fatalf("%s should not be locally handled", cmd)
		}
		if res.Rewrite != cmd {
			t.Fatalf("%s rewrite = %q want %q", cmd, res.Rewrite, cmd)
		}
	}
}

func TestRegistryUnknown(t *testing.T) {
	r := NewRegistry()
	res := r.Try("/nope", Context{})
	if !res.Handled || !strings.Contains(res.Response, "unknown command /nope") {
		t.Fatalf("/nope -> %+v", res)
	}
}

func TestRegistryCustomCommand(t *testing.T) {
	r := NewRegistry()
	r.Register("ping", "reply pong", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "pong:" + args}
	})
	res := r.Try("/ping hello", Context{})
	if !res.Handled || res.Response != "pong:hello" {
		t.Fatalf("/ping -> %+v", res)
	}
	// case-insensitive registration/lookup
	if res := r.Try("/PING z", Context{}); res.Response != "pong:z" {
		t.Fatalf("/PING case -> %+v", res)
	}
}
