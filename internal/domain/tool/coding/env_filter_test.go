package coding

import (
	"strings"
	"testing"
)

func TestSafeEnvFiltersSecrets(t *testing.T) {
	// Set test env vars
	t.Setenv("OPENAI_API_KEY", "sk-test123")
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("SECRET_API_TOKEN", "secret123")
	t.Setenv("TOKEN_MY_APP", "app123")
	t.Setenv("PASSWORD_DB", "dbpass")
	t.Setenv("CREDENTIALS_FILE", "/path/to/cred")
	t.Setenv("API_KEY_CUSTOM", "custom123")

	// Safe env vars
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/user")
	t.Setenv("GOPATH", "/home/user/go")
	t.Setenv("MY_NORMAL_VAR", "normal")

	result := SafeEnv()

	// Should NOT contain sensitive vars
	for _, e := range result {
		k, _, _ := strings.Cut(e, "=")
		sensitive := []string{
			"OPENAI_API_KEY", "GITHUB_TOKEN", "SECRET_API_TOKEN",
			"TOKEN_MY_APP", "PASSWORD_DB", "CREDENTIALS_FILE", "API_KEY_CUSTOM",
		}
		for _, s := range sensitive {
			if k == s {
				t.Errorf("SafeEnv should filter %s, but it's present", k)
			}
		}
	}

	// Should contain safe vars
	shouldContain := []string{"PATH", "GOPATH", "MY_NORMAL_VAR"}
	for _, expected := range shouldContain {
		found := false
		for _, e := range result {
			k, _, _ := strings.Cut(e, "=")
			if k == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SafeEnv should contain %s", expected)
		}
	}
}

func TestFilterEnvWithExtraKeep(t *testing.T) {
	t.Setenv("MY_SECRET", "secret123")
	t.Setenv("MY_NORMAL", "normal")

	env := []string{"MY_SECRET=secret123", "MY_NORMAL=normal", "PATH=/usr/bin"}
	result := FilterEnv(env, "MY_SECRET")

	// MY_SECRET should be kept because it's in extraKeep
	found := false
	for _, e := range result {
		if strings.HasPrefix(e, "MY_SECRET=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected MY_SECRET to be kept via extraKeep")
	}
}

func TestFilterEnvNil(t *testing.T) {
	// nil should use os.Environ()
	result := FilterEnv(nil)
	if len(result) == 0 {
		t.Error("expected non-empty result from os.Environ()")
	}
}
