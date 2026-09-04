package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

const (
	DefaultDreamMinSessions     = 3
	DefaultDreamInterval        = 4 * time.Hour
	DefaultDreamMinMemories     = 5
	DefaultDreamConsolidateMax  = 20
	DefaultDreamPruneImportance = 20
	DefaultDreamPruneAge        = 30 * 24 * time.Hour
)

type DreamConfig struct {
	Enabled         bool
	MinSessions     int
	Interval        time.Duration
	MinMemories     int
	ConsolidateMax  int
	PruneImportance int
	PruneAge        time.Duration
}

func DefaultDreamConfig() DreamConfig {
	return DreamConfig{
		Enabled:         true,
		MinSessions:     DefaultDreamMinSessions,
		Interval:        DefaultDreamInterval,
		MinMemories:     DefaultDreamMinMemories,
		ConsolidateMax:  DefaultDreamConsolidateMax,
		PruneImportance: DefaultDreamPruneImportance,
		PruneAge:        DefaultDreamPruneAge,
	}
}

type DreamResult struct {
	Consolidated int
	Pruned       int
	NewItemID    int64
	Summary      string
	Duration     time.Duration
}

func (s *Service) RunDreamConsolidation(ctx context.Context, cfg DreamConfig, projectID string) (*DreamResult, error) {
	start := time.Now()
	result := &DreamResult{}

	items, err := s.repo.List(ctx, projectID, memport.ScopeProject, cfg.ConsolidateMax)
	if err != nil || len(items) < cfg.MinMemories {
		return result, nil
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Importance > items[j].Importance
	})

	var contents []string
	for _, it := range items {
		contents = append(contents, fmt.Sprintf("[%s|imp=%d] %s", it.Category, it.Importance, it.Content))
	}

	if s.embedder == nil {
		return result, nil
	}

	dreamPrompt := buildDreamPrompt(contents)

	summary, err := s.consolidateWithLLM(ctx, dreamPrompt)
	if err != nil || summary == "" {
		summary = ruleBasedConsolidate(contents)
	}

	if summary != "" {
		item := &memport.MemoryItem{
			ProjectID:  projectID,
			Scope:      memport.ScopeProject,
			Category:   "dream_consolidation",
			Content:    summary,
			Importance: 75,
			Source:     "dream",
		}
		if err := s.Save(ctx, item); err == nil {
			result.NewItemID = item.ID
			result.Consolidated = len(contents)
			result.Summary = summary
		}
	}

	pruned, _ := s.repo.Prune(ctx, cfg.PruneImportance, time.Now().Add(-cfg.PruneAge))
	result.Pruned = pruned
	result.Duration = time.Since(start)

	return result, nil
}

func buildDreamPrompt(contents []string) string {
	var b strings.Builder
	b.WriteString("You are a memory consolidation system. Consolidate the following project memories into a coherent knowledge base entry.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Merge duplicates and near-duplicates\n")
	b.WriteString("- Keep unique information\n")
	b.WriteString("- Remove obsolete entries\n")
	b.WriteString("- Output a concise summary that captures all important facts\n\n")
	b.WriteString("Memories:\n")
	for _, c := range contents {
		b.WriteString("- " + c + "\n")
	}
	b.WriteString("\nConsolidated summary:\n")
	return b.String()
}

func (s *Service) consolidateWithLLM(ctx context.Context, prompt string) (string, error) {
	if s.extractor == nil {
		return "", fmt.Errorf("no extractor available")
	}
	return "", nil
}

func ruleBasedConsolidate(contents []string) string {
	if len(contents) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var unique []string
	for _, c := range contents {
		key := strings.ToLower(c)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, c)
		}
	}
	var b strings.Builder
	b.WriteString("Consolidated knowledge base:\n")
	for _, u := range unique {
		b.WriteString("- " + u + "\n")
	}
	return b.String()
}

func (s *Service) MaybeRunDreamConsolidation(ctx context.Context, cfg DreamConfig, projectID string, sessionCount int) (*DreamResult, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if sessionCount < cfg.MinSessions {
		return nil, nil
	}
	items, err := s.repo.List(ctx, projectID, memport.ScopeProject, cfg.MinMemories+1)
	if err != nil || len(items) < cfg.MinMemories {
		return nil, nil
	}
	return s.RunDreamConsolidation(ctx, cfg, projectID)
}
