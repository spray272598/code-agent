package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/infrastructure/config"
)

// MinIOStore implements blob.Store via minio-go (S3 API).
type MinIOStore struct {
	client *minio.Client
	bucket string
}

// NewStoreFromConfig prefers MinIO when endpoint reachable; else local fallback.
func NewStoreFromConfig(cfg config.StorageConfig) (blob.Store, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("storage disabled")
	}
	if cfg.Endpoint != "" && cfg.AccessKey != "" {
		ms, err := NewMinIOStore(cfg)
		if err == nil {
			log.Printf("[storage] using MinIO endpoint=%s bucket=%s\n", cfg.Endpoint, cfg.Bucket)
			return ms, nil
		}
		log.Printf("[storage] MinIO unavailable (%v), fallback local\n", err)
	}
	dir := cfg.LocalFallbackDir
	if dir == "" {
		dir = "./data/objects"
	}
	return NewLocalStore(dir)
}

func NewMinIOStore(cfg config.StorageConfig) (*MinIOStore, error) {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")
	endpoint = strings.TrimRight(endpoint, "/")
	secure := strings.HasPrefix(cfg.Endpoint, "https://")
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = "code-agent"
	}
	exists, err := cli.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("minio ping: %w", err)
	}
	if !exists {
		if err := cli.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("make bucket: %w", err)
		}
	}
	return &MinIOStore{client: cli, bucket: bucket}, nil
}

func (s *MinIOStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	key = strings.TrimPrefix(key, "/")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

func (s *MinIOStore) Get(ctx context.Context, key string) ([]byte, error) {
	key = strings.TrimPrefix(key, "/")
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

func (s *MinIOStore) Exists(ctx context.Context, key string) bool {
	key = strings.TrimPrefix(key, "/")
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	return err == nil
}

func (s *MinIOStore) Delete(ctx context.Context, key string) error {
	key = strings.TrimPrefix(key, "/")
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *MinIOStore) SignURL(ctx context.Context, key string, expireSec int) (string, error) {
	if expireSec <= 0 {
		expireSec = 3600
	}
	key = strings.TrimPrefix(key, "/")
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, time.Duration(expireSec)*time.Second, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
