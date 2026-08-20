// Package llmkey is the Sprint 2.3 abstraction for per-user LLM API key storage.
// Each user (org) can register one or more (provider, alias, api_key, api_base)
// tuples. api_key and api_base are stored encrypted at rest via KMS; this
// domain layer only sees plaintext.
package llmkey

import (
	"context"
	"errors"
)

// Key is a per-user LLM credential entry.
type Key struct {
	UserID   string
	Alias    string // user-facing name like "openai-prod"
	Provider string // "openai" | "anthropic" | "siliconflow" | ...
	APIKey   string // plaintext — never stored as such on disk
	APIBase  string // plaintext override of the upstream URL
	Enabled  bool
}

// Repository is the CRUD surface. All methods are ctx-driven; callers that
// don't carry a tenant must use the WithSystem variant to read shared entries.
type Repository interface {
	Save(ctx context.Context, k Key) error
	Get(ctx context.Context, userID, alias string) (*Key, error)
	ListByUser(ctx context.Context, userID string) ([]Key, error)
	Delete(ctx context.Context, userID, alias string) error
}

// ErrNotFound is returned by Get when no entry matches.
var ErrNotFound = errors.New("llm key not found")

// ErrDuplicate is returned by Save when alias already exists for the user.
var ErrDuplicate = errors.New("llm key alias already exists for this user")

// ErrTenantMissing is returned when ctx does not carry a userID and the repo
// was wired in multi-tenant mode (the default).
var ErrTenantMissing = errors.New("tenant missing from context")