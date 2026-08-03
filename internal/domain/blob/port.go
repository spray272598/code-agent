package blob

import "context"

// Store offloads large tool results / artifacts (domain port).
type Store interface {
	// Put stores bytes; returns stable key.
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get reads full object.
	Get(ctx context.Context, key string) ([]byte, error)
	// Exists reports whether key is present.
	Exists(ctx context.Context, key string) bool
}

// OffloadResult replaces large text with a short pointer for the LLM context.
type OffloadResult struct {
	Preview   string
	ObjectKey string
	Bytes     int
	Offloaded bool
}
