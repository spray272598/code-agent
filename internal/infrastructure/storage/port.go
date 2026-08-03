package storage

import (
	"context"
	"io"
)

// IObjectStorage S3-compatible object storage (MinIO / OSS / S3).
// Defined here for infra; domain depends on a thin port in domain if needed later.
type IObjectStorage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// SignURL optional temporary download URL.
	SignURL(ctx context.Context, key string, expireSec int) (string, error)
}
