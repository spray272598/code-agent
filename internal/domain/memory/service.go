package memory

import (
	"context"
	"fmt"
	"strings"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

// Service long-term memory: user + project scopes (walicode CoreMemory inspired).
type Service struct {
	repo memport.IMemoryRepository
}

func NewService(repo memport.IMemoryRepository) *Service {
	return &Service{repo: repo}
}

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
	return s.repo.Save(ctx, item)
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

func (s *Service) Search(ctx context.Context, userID, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	return s.repo.Search(ctx, userID, projectID, query, limit)
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

// MaybeExtractFromUserCorrection heuristic write on regret/preference phrases.
func (s *Service) MaybeExtractFromUserCorrection(ctx context.Context, userID, projectID, sessionID, text string) {
	if s == nil || userID == "" || text == "" {
		return
	}
	lower := strings.ToLower(text)
	triggers := []string{"不对", "不要", "记住", "以后", "偏好", "always", "prefer", "remember", "别再", "应该用"}
	hit := false
	for _, t := range triggers {
		if strings.Contains(lower, t) || strings.Contains(text, t) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	_ = s.Save(ctx, &memport.MemoryItem{
		UserID: userID, ProjectID: projectID, Scope: memport.ScopeUser,
		Category: "correction", Content: truncateRunes(text, 300),
		Importance: 70, Source: "auto:" + sessionID,
	})
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


