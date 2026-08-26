package memory

import (
	"context"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type MemoryBackend interface {
	Search(ctx context.Context, userID, projectID, query string, opts SearchOptions) ([]ScoredItem, error)
	Get(ctx context.Context, id int64) (*memport.MemoryItem, error)
	List(ctx context.Context, userID, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error)
	Save(ctx context.Context, item *memport.MemoryItem) error
	Delete(ctx context.Context, id int64) error
	TotalChunks(ctx context.Context) (int64, error)
	Reindex(ctx context.Context) error
	Prune(ctx context.Context, minImportance int, olderThan time.Time) (int, error)
}

type ServiceBackend struct {
	svc *Service
}

func NewServiceBackend(svc *Service) *ServiceBackend {
	return &ServiceBackend{svc: svc}
}

func (b *ServiceBackend) Search(ctx context.Context, userID, projectID, query string, opts SearchOptions) ([]ScoredItem, error) {
	return b.svc.HybridSearch(ctx, userID, projectID, query, opts)
}

func (b *ServiceBackend) Get(ctx context.Context, id int64) (*memport.MemoryItem, error) {
	items, err := b.svc.repo.List(ctx, "", "", memport.ScopeUser, 10000)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == id {
			return &it, nil
		}
	}
	return nil, nil
}

func (b *ServiceBackend) List(ctx context.Context, userID, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	return b.svc.repo.List(ctx, userID, projectID, scope, limit)
}

func (b *ServiceBackend) Save(ctx context.Context, item *memport.MemoryItem) error {
	return b.svc.Save(ctx, item)
}

func (b *ServiceBackend) Delete(ctx context.Context, id int64) error {
	return b.svc.repo.Delete(ctx, id)
}

func (b *ServiceBackend) TotalChunks(ctx context.Context) (int64, error) {
	items, err := b.svc.repo.List(ctx, "", "", "", 100000)
	if err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}

func (b *ServiceBackend) Reindex(ctx context.Context) error {
	if b.svc.embedder == nil {
		return nil
	}
	items, err := b.svc.repo.ListNoEmbedding(ctx, 1000)
	if err != nil {
		return err
	}
	for _, item := range items {
		vectors, err := b.svc.embedder.Embed(ctx, []string{item.Content})
		if err != nil || len(vectors) == 0 {
			continue
		}
		item.Embedding = vectors[0]
		_ = b.svc.repo.Save(ctx, &item)
	}
	return nil
}

func (b *ServiceBackend) Prune(ctx context.Context, minImportance int, olderThan time.Time) (int, error) {
	return b.svc.repo.Prune(ctx, minImportance, olderThan)
}

var _ MemoryBackend = (*ServiceBackend)(nil)
