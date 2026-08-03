package repository

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type MemoryCoreRepo struct {
	mu   sync.RWMutex
	seq  atomic.Int64
	data []memport.MemoryItem
}

func NewMemoryCoreRepo() *MemoryCoreRepo {
	return &MemoryCoreRepo{data: make([]memport.MemoryItem, 0)}
}

func (r *MemoryCoreRepo) Save(_ context.Context, item *memport.MemoryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.seq.Add(1)
	}
	// upsert by id
	for i := range r.data {
		if r.data[i].ID == item.ID {
			r.data[i] = *item
			return nil
		}
	}
	r.data = append(r.data, *item)
	return nil
}

func (r *MemoryCoreRepo) List(_ context.Context, userID, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []memport.MemoryItem
	for i := len(r.data) - 1; i >= 0; i-- {
		it := r.data[i]
		if it.UserID != userID {
			continue
		}
		if scope != "" && it.Scope != scope {
			continue
		}
		if scope == memport.ScopeProject && projectID != "" && it.ProjectID != projectID {
			continue
		}
		out = append(out, it)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	// sort by importance desc (simple insertion)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Importance > out[i].Importance {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (r *MemoryCoreRepo) Search(_ context.Context, userID, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tokens := memport.Tokenize(query)
	type scored struct {
		it    memport.MemoryItem
		score int
	}
	var ranked []scored
	for _, it := range r.data {
		if it.UserID != userID {
			continue
		}
		if it.Scope == memport.ScopeProject && projectID != "" && it.ProjectID != projectID && it.ProjectID != "" {
			// allow user-scope always; project-scope only matching project
			continue
		}
		if it.Scope == memport.ScopeProject && projectID != "" && it.ProjectID != projectID {
			continue
		}
		content := strings.ToLower(it.Content + " " + it.Category)
		sc := it.Importance / 10
		if query != "" && strings.Contains(content, strings.ToLower(query)) {
			sc += 50
		}
		for _, t := range tokens {
			if t != "" && strings.Contains(content, t) {
				sc += 10
			}
		}
		if sc > 0 || query == "" {
			ranked = append(ranked, scored{it: it, score: sc})
		}
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	if limit <= 0 {
		limit = 10
	}
	out := make([]memport.MemoryItem, 0, limit)
	for i := 0; i < len(ranked) && i < limit; i++ {
		out = append(out, ranked[i].it)
	}
	return out, nil
}

func (r *MemoryCoreRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.data {
		if r.data[i].ID == id {
			r.data = append(r.data[:i], r.data[i+1:]...)
			return nil
		}
	}
	return nil
}
