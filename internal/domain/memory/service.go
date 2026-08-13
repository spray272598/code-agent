package memory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

// Service long-term memory: user + project scopes (walicode CoreMemory inspired).
type Service struct {
	repo      memport.IMemoryRepository
	extractor Extractor
	embedder  port.IEmbeddingPort
}

func NewService(repo memport.IMemoryRepository) *Service {
	return &Service{repo: repo}
}

// SetExtractor injects a memory extractor (e.g. LLM-backed) for semantic
// extraction. When nil/unset, MaybeExtractFromUserCorrection falls back to rules.
func (s *Service) SetExtractor(e Extractor) { s.extractor = e }

// SetEmbedder injects an embedding port for semantic search. When nil, Save
// stores no vectors and Search degrades to keyword matching.
func (s *Service) SetEmbedder(e port.IEmbeddingPort) { s.embedder = e }

func (s *Service) Save(ctx context.Context, item *memport.MemoryItem) error {
	if s == nil || s.repo == nil || item == nil {
		return fmt.Errorf("memory unavailable")
	}
	if item.UserID == "" {
		return fmt.Errorf("userId required")
	}
	item.Content = strings.TrimSpace(item.Content)
	if item.Content == "" {
		return fmt.Errorf("content required")
	}
	if item.Scope == "" {
		item.Scope = memport.ScopeUser
	}
	if item.Scope == memport.ScopeProject && item.ProjectID == "" {
		return fmt.Errorf("projectId required for project scope")
	}
	if item.Category == "" {
		item.Category = "general"
	}
	if item.Importance <= 0 {
		item.Importance = 50
	}
	if item.Importance > 100 {
		item.Importance = 100
	}
	if item.Source == "" {
		item.Source = "manual"
	}
	// compute embedding when enabled and not already present
	if s.embedder != nil && len(item.Embedding) == 0 {
		if vecs, err := s.embedder.Embed(ctx, []string{item.Content}); err == nil && len(vecs) == 1 {
			item.Embedding = vecs[0]
		} else if err != nil {
			log.Printf("[memory] embed on save: %v", err)
		}
	}
	// dedupe + conflict resolution: overwrite an existing memory with nearly
	// identical semantics instead of piling up near-duplicates.
	if id, dup := s.findDuplicate(ctx, item, item.Embedding); dup {
		item.ID = id
	}
	return s.repo.Save(ctx, item)
}

// dupThreshold is the cosine similarity above which a new memory is treated as
// an update to an existing one (not a new memory).
const dupThreshold = 0.88

// findDuplicate returns the ID of the most semantically similar existing memory
// (within user+project scope) when its similarity exceeds dupThreshold.
func (s *Service) findDuplicate(ctx context.Context, item *memport.MemoryItem, emb []float32) (int64, bool) {
	if s.embedder == nil || len(emb) == 0 {
		return 0, false
	}
	existing, err := s.List(ctx, item.UserID, item.ProjectID, "", 200)
	if err != nil || len(existing) == 0 {
		return 0, false
	}
	bestID, bestSim := int64(0), 0.0
	for _, it := range existing {
		if it.ID == item.ID || len(it.Embedding) == 0 {
			continue
		}
		if sim := memport.CosineSimilarity(emb, it.Embedding); sim > bestSim {
			bestSim = sim
			bestID = it.ID
		}
	}
	if bestSim >= dupThreshold {
		return bestID, true
	}
	return 0, false
}

func (s *Service) List(ctx context.Context, userID, projectID string, scope memport.Scope, limit int) ([]memport.MemoryItem, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	return s.repo.List(ctx, userID, projectID, scope, limit)
}

// Backfill computes embeddings for stored memories that lack one (e.g. saved
// before embedding was enabled). Returns the number backfilled.
func (s *Service) Backfill(ctx context.Context, limit int) int {
	if s == nil || s.repo == nil || s.embedder == nil {
		return 0
	}
	if limit <= 0 {
		limit = 200
	}
	items, err := s.repo.ListNoEmbedding(ctx, limit)
	if err != nil {
		log.Printf("[memory] backfill list: %v", err)
		return 0
	}
	n := 0
	for _, it := range items {
		vecs, err := s.embedder.Embed(ctx, []string{it.Content})
		if err != nil || len(vecs) != 1 || len(vecs[0]) == 0 {
			continue
		}
		it.Embedding = vecs[0]
		if err := s.repo.Save(ctx, &it); err != nil {
			log.Printf("[memory] backfill save %d: %v", it.ID, err)
			continue
		}
		n++
	}
	return n
}

// Prune removes low-importance memories not used recently. Returns count removed.
func (s *Service) Prune(ctx context.Context, minImportance int, olderThan time.Time) int {
	if s == nil || s.repo == nil {
		return 0
	}
	n, err := s.repo.Prune(ctx, minImportance, olderThan)
	if err != nil {
		log.Printf("[memory] prune: %v", err)
		return 0
	}
	return n
}

func (s *Service) Search(ctx context.Context, userID, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	// no embedder → pure keyword search (unchanged behavior)
	if s.embedder == nil || query == "" {
		return s.repo.Search(ctx, userID, projectID, query, limit)
	}

	// hybrid: keyword recall (candidates) + cosine rerank on top
	qvecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(qvecs) != 1 || len(qvecs[0]) == 0 {
		// embedding unavailable → graceful keyword fallback
		return s.repo.Search(ctx, userID, projectID, query, limit)
	}
	qvec := qvecs[0]

	// recall a wider candidate set via keyword, then list all as fallback
	candidates, err := s.repo.Search(ctx, userID, projectID, query, limit*4)
	if err != nil {
		candidates, _ = s.repo.List(ctx, userID, projectID, "", limit*4)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// score = cosine similarity (+ small importance boost); rerank desc
	type scored struct {
		it    memport.MemoryItem
		score float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, it := range candidates {
		sim := 0.0
		if len(it.Embedding) > 0 {
			sim = memport.CosineSimilarity(qvec, it.Embedding)
		}
		score := sim + float64(it.Importance)/10000 // tiny tiebreak, keep semantic dominant
		ranked = append(ranked, scored{it: it, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	out := make([]memport.MemoryItem, 0, limit)
	for i := 0; i < len(ranked) && i < limit; i++ {
		out = append(out, ranked[i].it)
	}
	return out, nil
}

// FormatForPrompt retrieves top memories for user+project and formats for system prompt.
func (s *Service) FormatForPrompt(ctx context.Context, userID, projectID, query string, limit int) string {
	if s == nil || s.repo == nil || userID == "" {
		return ""
	}
	if limit <= 0 {
		limit = 8
	}
	var items []memport.MemoryItem
	if query != "" {
		found, err := s.Search(ctx, userID, projectID, query, limit)
		if err == nil {
			items = found
		}
	}
	if len(items) < limit {
		// merge user + project lists by importance
		userItems, _ := s.List(ctx, userID, "", memport.ScopeUser, limit)
		projItems, _ := s.List(ctx, userID, projectID, memport.ScopeProject, limit)
		items = mergeUnique(items, userItems, projItems, limit)
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Long-term memory (user + project)\n")
	b.WriteString("Respect these facts/preferences unless the user overrides them.\n")
	for _, it := range items {
		b.WriteString(fmt.Sprintf("- [%s|%s|imp=%d] %s\n", it.Scope, it.Category, it.Importance, it.Content))
	}
	return b.String()
}

// memoryIntentTriggers are a cheap pre-filter to avoid calling the LLM on every
// turn. They only decide *whether to consider extraction*; the LLM (when present)
// does the precise semantic judgment and structuring.
var memoryIntentTriggers = []string{
	"不对", "不要", "记住", "以后", "偏好", "别再", "应该用", "改成", "换成",
	"always", "prefer", "remember", "from now on", "instead of",
}

func looksMemoryIntent(text string) bool {
	lower := strings.ToLower(text)
	for _, t := range memoryIntentTriggers {
		if strings.Contains(lower, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// MaybeExtractFromUserCorrection extracts durable memories from user input.
//
// Strategy: a cheap trigger pre-filter first; then, when an LLM extractor is
// configured, delegate to it for precise semantic judgment + structured output;
// finally fall back to the legacy raw-text save when the LLM is absent or fails.
func (s *Service) MaybeExtractFromUserCorrection(ctx context.Context, userID, projectID, sessionID, text string) {
	if s == nil || s.repo == nil || userID == "" || text == "" {
		return
	}
	// cheap pre-filter: skip obviously non-memory turns without any LLM cost
	if !looksMemoryIntent(text) {
		return
	}

	// semantic extraction path (structured, deduped by category/content)
	if s.extractor != nil {
		items, err := s.extractor.Extract(ctx, text)
		if err == nil {
			for _, it := range items {
				if strings.TrimSpace(it.Content) == "" {
					continue
				}
				imp := it.Importance
				if imp <= 0 {
					imp = 70
				}
				if imp > 100 {
					imp = 100
				}
				if err := s.Save(ctx, &memport.MemoryItem{
					UserID: userID, ProjectID: projectID, Scope: memport.ScopeUser,
					Category: it.Category, Content: it.Content,
					Importance: imp, Source: "auto:" + sessionID,
				}); err != nil {
					log.Printf("[error] memory auto-save (llm): %v", err)
				}
			}
			return // LLM already judged; even an empty result means "nothing to store"
		}
		// LLM error → fall through to rule fallback
		log.Printf("[memory] extractor failed, fallback to rule: %v", err)
	}

	// rule fallback: keep legacy behavior (raw text) so memory never regresses
	if err := s.Save(ctx, &memport.MemoryItem{
		UserID: userID, ProjectID: projectID, Scope: memport.ScopeUser,
		Category: "correction", Content: truncateRunes(text, 300),
		Importance: 70, Source: "auto:" + sessionID,
	}); err != nil {
		log.Printf("[error] memory auto-save correction: %v", err)
	}
}

func mergeUnique(base, a, b []memport.MemoryItem, limit int) []memport.MemoryItem {
	seen := map[string]bool{}
	var out []memport.MemoryItem
	add := func(list []memport.MemoryItem) {
		for _, it := range list {
			key := strings.ToLower(it.Content)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, it)
			if len(out) >= limit {
				return
			}
		}
	}
	add(base)
	add(a)
	add(b)
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
