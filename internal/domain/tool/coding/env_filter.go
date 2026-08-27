package coding

import (
	"os"
	"strings"
)

// sensitiveEnvKeys is a set of environment variable prefixes/names that must
// NOT be forwarded to child processes (bash, MCP servers, etc.).
var sensitiveEnvKeys = map[string]bool{
	// LLM / AI
	"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true, "XAI_API_KEY": true,
	"GROK_API_KEY": true, "DEEPSEEK_API_KEY": true, "SILICONFLOW_API_KEY": true,
	"LLM_API_KEY": true,
	// Cloud providers
	"AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true,
	"AZURE_CLIENT_SECRET": true, "GCP_SERVICE_ACCOUNT_KEY": true,
	"GOOGLE_APPLICATION_CREDENTIALS": true,
	// Tokens / auth
	"GITHUB_TOKEN": true, "GH_TOKEN": true, "GITLAB_TOKEN": true,
	"SLACK_TOKEN": true, "DISCORD_TOKEN": true, "BEARER_TOKEN": true,
	"CODE_AGENT_API_KEY": true,
	// Database
	"MYSQL_ROOT_PASSWORD": true, "POSTGRES_PASSWORD": true, "DB_PASSWORD": true,
	"REDIS_PASSWORD": true,
}

// sensitiveEnvPrefixes are env var prefixes that are always stripped.
var sensitiveEnvPrefixes = []string{
	"SECRET_", "TOKEN_", "PASSWORD_", "CREDENTIALS_", "API_KEY_",
}

// SafeEnv returns a copy of os.Environ() with sensitive variables removed.
func SafeEnv() []string {
	return FilterEnv(os.Environ())
}

// FilterEnv filters the given env slice and returns only safe variables.
// Keys in extraKeep are always preserved regardless of sensitivity rules.
func FilterEnv(env []string, extraKeep ...string) []string {
	if env == nil {
		env = os.Environ()
	}
	keep := make(map[string]bool, len(extraKeep))
	for _, k := range extraKeep {
		keep[k] = true
	}
	var result []string
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if keep[k] {
			result = append(result, e)
			continue
		}
		if sensitiveEnvKeys[k] {
			continue
		}
		skip := false
		for _, pfx := range sensitiveEnvPrefixes {
			if strings.HasPrefix(k, pfx) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		result = append(result, e)
	}
	return result
}
