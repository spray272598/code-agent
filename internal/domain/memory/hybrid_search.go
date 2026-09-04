package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type SearchOptions struct {
	Limit      int
	MinScore   float64
	UseFTS     bool
	UseVector  bool
	Scope      memport.Scope
	Category   string
	ExcludeIDs []int64
	TimeDecay  float64
}

func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		Limit:     10,
		MinScore:  0.0,
		UseFTS:    true,
		UseVector: true,
		TimeDecay: 0.1,
	}
}

type ScoredItem struct {
	Item  memport.MemoryItem
	Score float64
}

func (s *Service) HybridSearch(ctx context.Context, projectID, query string, opts SearchOptions) ([]ScoredItem, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}

	var ftsResults []ScoredItem
	var vectorResults []ScoredItem

	if opts.UseFTS {
		items, err := s.repo.Search(ctx, projectID, query, opts.Limit*2)
		if err == nil {
			for _, it := range items {
				score := computeFTSScore(query, it)
				if score >= opts.MinScore {
					ftsResults = append(ftsResults, ScoredItem{Item: it, Score: score})
				}
			}
		}
	}

	if opts.UseVector && s.vector != nil && s.embedder != nil {
		vectorResults = s.vectorSearch(ctx, projectID, query, opts)
	}

	merged := mergeSearchResults(ftsResults, vectorResults, opts)

	if opts.TimeDecay > 0 {
		now := time.Now()
		for i := range merged {
			ageHours := now.Sub(merged[i].Item.CreatedAt).Hours()
			decay := 1.0 / (1.0 + opts.TimeDecay*ageHours/24.0)
			merged[i].Score *= decay
		}
	}

	excluded := make(map[int64]bool)
	for _, id := range opts.ExcludeIDs {
		excluded[id] = true
	}
	var filtered []ScoredItem
	for _, r := range merged {
		if !excluded[r.Item.ID] {
			filtered = append(filtered, r)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}

	return filtered, nil
}

func computeFTSScore(query string, item memport.MemoryItem) float64 {
	qTokens := memport.Tokenize(query)
	content := strings.ToLower(item.Content)
	category := strings.ToLower(item.Category)

	matches := 0.0
	for _, token := range qTokens {
		if strings.Contains(content, token) {
			matches += 1.0
		}
		if category == token {
			matches += 0.5
		}
	}

	if len(qTokens) == 0 {
		return 0.0
	}

	importanceBoost := float64(item.Importance) / 100.0
	score := (matches/float64(len(qTokens)))*0.7 + importanceBoost*0.3
	return score
}

func (s *Service) vectorSearch(ctx context.Context, projectID, query string, opts SearchOptions) []ScoredItem {
	if s.vector == nil || s.embedder == nil {
		return nil
	}

	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 {
		return nil
	}
	queryVec := vecs[0]

	results, err := s.vector.Search(ctx, s.collection, queryVec, opts.Limit*2, map[string]any{
		"project_id": projectID,
	})
	if err != nil {
		return nil
	}

	var scored []ScoredItem
	for _, r := range results {
		var id int64
		switch v := r.Payload["id"].(type) {
		case int64:
			id = v
		case float64:
			id = int64(v)
		case string:
			fmt.Sscanf(v, "%d", &id)
		}
		content, _ := r.Payload["content"].(string)
		category, _ := r.Payload["category"].(string)
		importance := 50
		if imp, ok := r.Payload["importance"].(float64); ok {
			importance = int(imp)
		}

		item := memport.MemoryItem{
			ID: id, ProjectID: projectID,
			Content: content, Category: category, Importance: importance,
			Scope: memport.ScopeProject,
		}
		score := float64(r.Score)
		if score > 1.0 {
			score = score / (score + 1.0)
		}
		scored = append(scored, ScoredItem{Item: item, Score: score})
	}
	return scored
}

func mergeSearchResults(fts, vector []ScoredItem, opts SearchOptions) []ScoredItem {
	seen := make(map[int64]int)
	var merged []ScoredItem

	for _, r := range fts {
		if idx, ok := seen[r.Item.ID]; ok {
			merged[idx].Score = (merged[idx].Score + r.Score) / 2
		} else {
			seen[r.Item.ID] = len(merged)
			merged = append(merged, r)
		}
	}

	for _, r := range vector {
		if idx, ok := seen[r.Item.ID]; ok {
			if r.Score > merged[idx].Score {
				merged[idx].Score = (merged[idx].Score + r.Score) / 2
			}
		} else {
			seen[r.Item.ID] = len(merged)
			merged = append(merged, r)
		}
	}

	return merged
}

func (s *Service) FormatForPromptExtended(ctx context.Context, projectID, query string, limit int, opts SearchOptions) string {
	if s == nil {
		return ""
	}
	if limit <= 0 {
		limit = 8
	}
	opts.Limit = limit

	scored, err := s.HybridSearch(ctx, projectID, query, opts)
	if err != nil || len(scored) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<memory-context>\n")
	b.WriteString("Relevant memories from past sessions:\n")
	for _, sc := range scored {
		b.WriteString(fmt.Sprintf("- [score=%.2f|%s|%s|imp=%d] %s\n",
			sc.Score, sc.Item.Scope, sc.Item.Category, sc.Item.Importance, sc.Item.Content))
	}
	b.WriteString("</memory-context>")
	return b.String()
}
