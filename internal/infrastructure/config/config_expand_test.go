package config

import (
	"os"
	"testing"
)

// setEnv sets the given env vars and returns a function that unsets them again,
// so each case leaves the process environment untouched for the next.
func setEnv(m map[string]string) func() {
	for k, v := range m {
		_ = os.Setenv(k, v)
	}
	return func() {
		for k := range m {
			_ = os.Unsetenv(k)
		}
	}
}

func TestExpandEnvPlaceholders(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		in   string
		want string
	}{
		{"unset no default", nil, "${MISSING_VAR_XYZ}", ""},
		{"unset with default", nil, "${MISSING_VAR_XYZ:-fallback}", "fallback"},
		{"set no default", map[string]string{"SET_VAR_XYZ": "v"}, "${SET_VAR_XYZ}", "v"},
		{"set with default", map[string]string{"SET_VAR_XYZ": "v"}, "${SET_VAR_XYZ:-fb}", "v"},
		{"set empty uses default", map[string]string{"SET_VAR_XYZ": ""}, "${SET_VAR_XYZ:-fb}", "fb"},
		{"keeps surrounding text", nil, "pre-${MISSING_VAR_XYZ:-x}-post", "pre-x-post"},
		{"multiple placeholders", map[string]string{"A_XYZ": "1", "B_XYZ": "2"}, "${A_XYZ:-a}-${B_XYZ:-b}", "1-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			undo := setEnv(c.env)
			defer undo()
			if got := string(expandEnvPlaceholders([]byte(c.in))); got != c.want {
				t.Fatalf("expandEnvPlaceholders(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLoad_ResolvesPlaceholdersFromFile(t *testing.T) {
	_ = os.Setenv("CODE_AGENT_TEST_KEY", "resolved-secret")
	defer os.Unsetenv("CODE_AGENT_TEST_KEY")

	dir := t.TempDir()
	p := dir + "/cfg.yaml"
	// server.host uses ${VAR:-default}; the value must come from the environment.
	content := "server:\n  host: \"${CODE_AGENT_TEST_KEY:-default-host}\"\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "resolved-secret" {
		t.Fatalf("placeholder not resolved: server.host = %q", cfg.Server.Host)
	}
}

func TestLoad_ResolvesPlaceholderDefaultWhenUnset(t *testing.T) {
	_ = os.Unsetenv("CODE_AGENT_TEST_KEY_MISSING")

	dir := t.TempDir()
	p := dir + "/cfg.yaml"
	content := "server:\n  host: \"${CODE_AGENT_TEST_KEY_MISSING:-default-host}\"\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "default-host" {
		t.Fatalf("placeholder default not applied: server.host = %q", cfg.Server.Host)
	}
}
