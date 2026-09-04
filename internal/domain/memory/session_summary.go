package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

const (
	DefaultTitleRefreshTurns = 5
	DefaultTitleMinLength    = 10
	DefaultTitleMaxLength    = 80
)

type SessionSummary struct {
	mu             sync.RWMutex
	titles         map[string]string
	refreshWater   map[string]int
	completionMark map[string]bool
}

func NewSessionSummary() *SessionSummary {
	return &SessionSummary{
		titles:         make(map[string]string),
		refreshWater:   make(map[string]int),
		completionMark: make(map[string]bool),
	}
}

func (s *SessionSummary) GetTitle(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.titles[sessionID]
}

func (s *SessionSummary) SetTitle(sessionID, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titles[sessionID] = truncateTitle(title)
}

func (s *SessionSummary) MarkComplete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completionMark[sessionID] = true
}

func (s *SessionSummary) IsComplete(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.completionMark[sessionID]
}

func (s *SessionSummary) Reset(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.titles, sessionID)
	delete(s.refreshWater, sessionID)
	s.completionMark[sessionID] = false
}

func (s *SessionSummary) ShouldRefresh(sessionID string, turnCount int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	watermark := s.refreshWater[sessionID]
	return turnCount-watermark >= DefaultTitleRefreshTurns
}

func (s *SessionSummary) UpdateWatermark(sessionID string, turnCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshWater[sessionID] = turnCount
}

func (s *SessionSummary) GenerateTitleFromMessages(messages []struct{ Role, Content string }) string {
	var b strings.Builder
	for _, m := range messages {
		if m.Role == "user" {
			b.WriteString(m.Content)
			break
		}
	}
	title := ruleBasedTitle(b.String())
	if title == "" {
		title = "Untitled Session"
	}
	return title
}

func ruleBasedTitle(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	runes := []rune(content)
	if len(runes) > DefaultTitleMaxLength {
		content = string(runes[:DefaultTitleMaxLength])
	}

	firstLine := strings.SplitN(content, "\n", 2)[0]
	firstLine = strings.TrimSpace(firstLine)
	if len([]rune(firstLine)) > DefaultTitleMaxLength {
		firstLine = string([]rune(firstLine)[:DefaultTitleMaxLength])
	}

	firstLine = strings.Trim(firstLine, "。.!?,.!? ")
	if len([]rune(firstLine)) < DefaultTitleMinLength {
		return ""
	}

	if strings.HasSuffix(firstLine, "...") {
		return firstLine
	}
	return firstLine
}

func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= DefaultTitleMaxLength {
		return title
	}
	return string(runes[:DefaultTitleMaxLength]) + "..."
}

type TitleGenerator struct {
	summary *SessionSummary
	memSvc  *Service
}

func NewTitleGenerator(summary *SessionSummary, memSvc *Service) *TitleGenerator {
	return &TitleGenerator{summary: summary, memSvc: memSvc}
}

func (tg *TitleGenerator) Update(ctx context.Context, sessionID, projectID string, messages []struct{ Role, Content string }, turnCount int) error {
	if !tg.summary.ShouldRefresh(sessionID, turnCount) {
		return nil
	}

	title := tg.summary.GetTitle(sessionID)
	if title == "" || tg.summary.IsComplete(sessionID) {
		newTitle := tg.summary.GenerateTitleFromMessages(messages)
		if newTitle != "" {
			tg.summary.SetTitle(sessionID, newTitle)
			tg.summary.UpdateWatermark(sessionID, turnCount)
			if tg.memSvc != nil {
				_ = tg.memSvc.Save(ctx, &memport.MemoryItem{
					ProjectID: projectID,
					Scope: memport.ScopeProject, Category: "session_title",
					Content:    fmt.Sprintf("Session %s: %s", sessionID, newTitle),
					Importance: 30, Source: "title_gen",
				})
			}
		}
	}
	return nil
}

func (tg *TitleGenerator) MarkDone(sessionID string) {
	tg.summary.MarkComplete(sessionID)
}

func (tg *TitleGenerator) Reset(sessionID string) {
	tg.summary.Reset(sessionID)
}

var _ = time.Now
