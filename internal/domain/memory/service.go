package memory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tenant"
	"github.com/spray272598/code-agent/internal/domain/vector"
)

// ErrTenantMissing is returned by ctx-driven entry points (SearchCtx) when the
// caller is not authenticated. Business code that flows through authJWT never
// sees this; tests and admin tools must inject a tenant via tenant.With.
var ErrTenantMissing = errors.New("tenant missing from context")

// Service long-term memory: user + project scopes (walicode CoreMemory inspired).
type Service struct {
	repo       memport.IMemoryRepository
	extractor  Extractor
	embedder   port.IEmbeddingPort
	vector     vector.IVectorIndex // Sprint 1.10: optional dense-vector backend
	collection string              // collection name when vector is set
}

func NewService(repo memport.IMemoryRepository) *Service {
	return &Service{repo: repo}
}

// SetVectorIndex wires an optional dense-vector backend (Sprint 1.10). When
// set, Search and findDuplicate prefer vector search with a strict user_id
// payload filter; on ErrUnavailable or any other error they fall back to the
// in-process cosine rerank so behavior degrades gracefully. Save additionally
// upserts new items into the index (best-effort, errors are logged not raised).
func (s *Service) SetVectorIndex(idx vector.IVectorIndex, collection string) {
	s.vector = idx
	s.collection = collection
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
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
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
	if err := s.repo.Save(ctx, item); err != nil {
		return err
	}
	// Best-effort vector upsert (Sprint 1.10): keeps the dense index in sync.
	s.indexOne(ctx, item)
	return nil
}

// dupThreshold is the cosine similarity above which a new memory is treated as
// an update to an existing one (not a new memory).
const dupThreshold = 0.88

const (
	halfLifeDays = 30.0

	sourceWeightGlobal   = 1.2
	sourceWeightProject  = 1.0
	sourceWeightSession  = 0.8
	sourceWeightDefault  = 1.0
)

var evergreenSources = map[string]bool{
	"global":  true,
	"project": true,
}

func SourceWeight(source string) float64 {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "global":
		return sourceWeightGlobal
	case "project":
		return sourceWeightProject
	case "session":
		return sourceWeightSession
	default:
		return sourceWeightDefault
	}
}

func IsEvergreen(source string) bool {
	return evergreenSources[strings.ToLower(strings.TrimSpace(source))]
}

func TemporalDecay(createdAt time.Time, source string) float64 {
	if IsEvergreen(source) || createdAt.IsZero() {
		return 1.0
	}
	ageDays := time.Since(createdAt).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Exp(-math.Ln2 / halfLifeDays * ageDays)
}

// findDuplicate returns the ID of the most semantically similar existing memory
// (within user+project scope) when its similarity exceeds dupThreshold.
// Sprint 1.10: prefers the vector index with a strict user_id payload filter
// (multi-tenant safe); on ErrUnavailable or any error falls back to the
// in-process cosine over List.
func (s *Service) findDuplicate(ctx context.Context, item *memport.MemoryItem, emb []float32) (int64, bool) {
	if s.embedder == nil || len(emb) == 0 {
		return 0, false
	}
	if s.vector != nil && s.collection != "" && item.UserID != "" {
		filter := map[string]any{"user_id": item.UserID}
		hits, err := s.vector.Search(ctx, s.collection, emb, 5, filter)
		if err == nil {
			for _, h := range hits {
				if h.Score >= dupThreshold {
					if id, perr := strconv.ParseInt(h.ID, 10, 64); perr == nil && id != item.ID {
						return id, true
					}
				}
			}
			return 0, false
		}
		if !errors.Is(err, vector.ErrUnavailable) {
			log.Printf("[memory] vector findDuplicate: %v", err)
		}
		// fall through to legacy path
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

	expandedQuery := ExpandQuery(query)

	if s.embedder == nil || query == "" {
		keywordQuery := expandedQuery
		if keywordQuery == "" {
			keywordQuery = query
		}
		return s.repo.Search(ctx, userID, projectID, keywordQuery, limit)
	}

	qvecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(qvecs) != 1 || len(qvecs[0]) == 0 {
		keywordQuery := expandedQuery
		if keywordQuery == "" {
			keywordQuery = query
		}
		return s.repo.Search(ctx, userID, projectID, keywordQuery, limit)
	}
	qvec := qvecs[0]

	recallQuery := expandedQuery
	if recallQuery == "" {
		recallQuery = query
	}
	candidates, err := s.repo.Search(ctx, userID, projectID, recallQuery, limit*4)
	if err != nil {
		candidates, _ = s.repo.List(ctx, userID, projectID, "", limit*4)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	var idOrder map[int64]float32
	if s.vector != nil && s.collection != "" && userID != "" {
		filter := map[string]any{"user_id": userID}
		hits, verr := s.vector.Search(ctx, s.collection, qvec, limit*4, filter)
		if verr == nil {
			idOrder = make(map[int64]float32, len(hits))
			for _, h := range hits {
				if id, perr := strconv.ParseInt(h.ID, 10, 64); perr == nil {
					idOrder[id] = h.Score
				}
			}
		} else if !errors.Is(verr, vector.ErrUnavailable) {
			log.Printf("[memory] vector search: %v", verr)
		}
	}

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
		decay := TemporalDecay(it.CreatedAt, it.Source)
		sw := SourceWeight(it.Source)
		score := sim * decay * sw + float64(it.Importance)/10000
		if v, ok := idOrder[it.ID]; ok {
			score += float64(v) + 0.001
		}
		ranked = append(ranked, scored{it: it, score: score})
	}

	items := make([]memport.MemoryItem, len(ranked))
	scores := make([]float64, len(ranked))
	for i, r := range ranked {
		items[i] = r.it
		scores[i] = r.score
	}

	mmrItems := MMRReRank(items, scores, defaultMMRLambda)

	out := make([]memport.MemoryItem, 0, limit)
	for i := 0; i < len(mmrItems) && i < limit; i++ {
		out = append(out, mmrItems[i])
	}
	return out, nil
}

// SearchCtx is the ctx-driven (Sprint 1.6/1.10) entry point: derives userID
// from tenant.From(ctx). Returns ErrTenantMissing when ctx is unauthenticated.
func (s *Service) SearchCtx(ctx context.Context, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	t, ok := tenant.From(ctx)
	if !ok || t.UserID == "" {
		return nil, ErrTenantMissing
	}
	return s.Search(ctx, t.UserID, projectID, query, limit)
}

// BackfillVector (Sprint 1.11) ensures the dense-vector index reflects every
// memory that has an embedding. For items lacking one, it embeds them on the
// fly (when an embedder is configured) and persists the embedding. Returns
// the number of items upserted. Errors from the vector backend are logged, not
// returned, so a misconfigured backend never blocks startup.
func (s *Service) BackfillVector(ctx context.Context, limit int) int {
	if s == nil || s.repo == nil {
		return 0
	}
	if s.vector == nil || s.collection == "" {
		return 0
	}
	if limit <= 0 {
		limit = 200
	}
	items, err := s.repo.ListNoEmbedding(ctx, limit)
	if err != nil {
		log.Printf("[memory] backfill-vector list: %v", err)
		return 0
	}
	n := 0
	for _, it := range items {
		if it.UserID == "" {
			continue
		}
		if len(it.Embedding) == 0 {
			if s.embedder == nil {
				continue
			}
			vecs, eerr := s.embedder.Embed(ctx, []string{it.Content})
			if eerr != nil || len(vecs) != 1 || len(vecs[0]) == 0 {
				log.Printf("[memory] backfill-vector embed %d: %v", it.ID, eerr)
				continue
			}
			it.Embedding = vecs[0]
			if serr := s.repo.Save(ctx, &it); serr != nil {
				log.Printf("[memory] backfill-vector save %d: %v", it.ID, serr)
				continue
			}
		}
		if uerr := s.vector.Upsert(ctx, s.collection, []vector.Point{pointOf(&it)}); uerr != nil {
			log.Printf("[memory] backfill-vector upsert %d: %v", it.ID, uerr)
			continue
		}
		n++
	}
	return n
}

// indexOne is the best-effort vector upsert after a successful Save.
func (s *Service) indexOne(ctx context.Context, item *memport.MemoryItem) {
	if s.vector == nil || s.collection == "" || item == nil {
		return
	}
	if item.UserID == "" || len(item.Embedding) == 0 {
		return
	}
	if err := s.vector.Upsert(ctx, s.collection, []vector.Point{pointOf(item)}); err != nil && !errors.Is(err, vector.ErrUnavailable) {
		log.Printf("[memory] vector upsert %d: %v", item.ID, err)
	}
}

func pointOf(it *memport.MemoryItem) vector.Point {
	return vector.Point{
		ID:     strconv.FormatInt(it.ID, 10),
		Vector: it.Embedding,
		Payload: map[string]any{
			"user_id":    it.UserID,
			"project_id": it.ProjectID,
			"scope":      string(it.Scope),
		},
	}
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
