package repository

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

// MemoryCoreRepo in-memory store with inverted index + importance/LRU eviction.
type MemoryCoreRepo struct {
	mu       sync.RWMutex
	seq      atomic.Int64
	data     []memItem
	index    map[string]map[int64]struct{} // token -> set of ids
	maxItems int
}

type memItem struct {
	item     memport.MemoryItem
	lastUsed time.Time
}

func NewMemoryCoreRepo() *MemoryCoreRepo {
	return &MemoryCoreRepo{
		data:     make([]memItem, 0),
		index:    map[string]map[int64]struct{}{},
		maxItems: 2000,
	}
}

// SetMaxItems for eviction cap (0 = default 2000).
func (r *MemoryCoreRepo) SetMaxItems(n int) {
	if n > 0 {
		r.maxItems = n
	}
}

func (r *MemoryCoreRepo) Save(_ context.Context, item *memport.MemoryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.seq.Add(1)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	cp := *item
	if len(cp.Embedding) > 0 {
		cp.Embedding = append([]float32(nil), cp.Embedding...)
	}
	for i := range r.data {
		if r.data[i].item.ID == cp.ID {
			r.unindex(r.data[i].item)
			r.data[i] = memItem{item: cp, lastUsed: time.Now()}
			r.indexItem(cp)
			return nil
		}
	}
	r.data = append(r.data, memItem{item: cp, lastUsed: time.Now()})
	r.indexItem(cp)
	r.evictLocked()
	return nil
}

func (r *MemoryCoreRepo) List(_ context.Context, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []memport.MemoryItem
	for i := len(r.data) - 1; i >= 0; i-- {
		it := r.data[i].item
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
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Importance > out[i].Importance {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (r *MemoryCoreRepo) Search(_ context.Context, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tokens := memport.Tokenize(query)
	type scored struct {
		idx   int
		score int
	}
	// candidate ids from inverted index
	candidates := map[int64]int{}
	if len(tokens) == 0 {
		for i := range r.data {
			candidates[r.data[i].item.ID] = 1
		}
	} else {
		for _, t := range tokens {
			if ids, ok := r.index[t]; ok {
				for id := range ids {
					candidates[id] += 10
				}
			}
		}
	}
	var ranked []scored
	for i, mi := range r.data {
		it := mi.item
		if it.Scope == memport.ScopeProject && projectID != "" && it.ProjectID != projectID {
			continue
		}
		base, ok := candidates[it.ID]
		if !ok && query != "" {
			// fallback substring for short queries / CJK
			content := strings.ToLower(it.Content + " " + it.Category)
			if query != "" && strings.Contains(content, strings.ToLower(query)) {
				base = 50
			} else {
				continue
			}
		}
		sc := base + it.Importance/10
		if query != "" && strings.Contains(strings.ToLower(it.Content), strings.ToLower(query)) {
			sc += 50
		}
		// recency boost
		age := time.Since(mi.lastUsed)
		if age < time.Hour {
			sc += 5
		}
		ranked = append(ranked, scored{idx: i, score: sc})
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
	now := time.Now()
	for i := 0; i < len(ranked) && i < limit; i++ {
		r.data[ranked[i].idx].lastUsed = now
		out = append(out, r.data[ranked[i].idx].item)
	}
	return out, nil
}

func (r *MemoryCoreRepo) ListNoEmbedding(_ context.Context, limit int) ([]memport.MemoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	var out []memport.MemoryItem
	for _, mi := range r.data {
		if len(mi.item.Embedding) == 0 {
			out = append(out, mi.item)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (r *MemoryCoreRepo) Prune(_ context.Context, minImportance int, olderThan time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	kept := make([]memItem, 0, len(r.data))
	for _, mi := range r.data {
		if mi.item.Importance < minImportance && mi.lastUsed.Before(olderThan) {
			r.unindex(mi.item)
			removed++
			continue
		}
		kept = append(kept, mi)
	}
	r.data = kept
	return removed, nil
}

func (r *MemoryCoreRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.data {
		if r.data[i].item.ID == id {
			r.unindex(r.data[i].item)
			r.data = append(r.data[:i], r.data[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *MemoryCoreRepo) indexItem(it memport.MemoryItem) {
	for _, t := range memport.Tokenize(it.Content + " " + it.Category) {
		if t == "" {
			continue
		}
		if r.index[t] == nil {
			r.index[t] = map[int64]struct{}{}
		}
		r.index[t][it.ID] = struct{}{}
	}
}

func (r *MemoryCoreRepo) unindex(it memport.MemoryItem) {
	for _, t := range memport.Tokenize(it.Content + " " + it.Category) {
		if ids, ok := r.index[t]; ok {
			delete(ids, it.ID)
			if len(ids) == 0 {
				delete(r.index, t)
			}
		}
	}
}

// evictLocked: importance-based + LRU when over maxItems.
// Protect high importance (>=80); drop lowest score first.
func (r *MemoryCoreRepo) evictLocked() {
	for len(r.data) > r.maxItems {
		worst := -1
		worstScore := 1 << 30
		now := time.Now()
		for i, mi := range r.data {
			if mi.item.Importance >= 80 {
				continue // never auto-evict critical
			}
			// score: higher = keep; lower = evict first
			ageMin := int(now.Sub(mi.lastUsed).Minutes())
			sc := mi.item.Importance*10 - ageMin
			if sc < worstScore {
				worstScore = sc
				worst = i
			}
		}
		if worst < 0 {
			// all critical — drop oldest non-critical by lastUsed among all
			worst = 0
			for i := 1; i < len(r.data); i++ {
				if r.data[i].lastUsed.Before(r.data[worst].lastUsed) && r.data[i].item.Importance < 80 {
					worst = i
				}
			}
			if r.data[worst].item.Importance >= 80 {
				return // cannot free
			}
		}
		r.unindex(r.data[worst].item)
		r.data = append(r.data[:worst], r.data[worst+1:]...)
	}
}
