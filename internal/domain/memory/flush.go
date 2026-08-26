package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type FlushConfig struct {
	Enabled    bool
	WindowSize int
	MinImportance int
	AutoExtract bool
}

func DefaultFlushConfig() FlushConfig {
	return FlushConfig{
		Enabled:       true,
		WindowSize:    20,
		MinImportance: 40,
		AutoExtract:   true,
	}
}

type FlushResult struct {
	Flushed   int
	Extracted int
	Skipped   int
	Duration  time.Duration
}

type ConversationTurn struct {
	Role    string
	Content string
	Time    time.Time
}

type FlushState struct {
	mu             sync.RWMutex
	enabled        bool
	flushLock      sync.Mutex
	flushCount     int64
	lastFlushTime  time.Time
	failedAttempts int64
}

func NewFlushState() *FlushState {
	return &FlushState{enabled: true}
}

func (f *FlushState) IsEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.enabled
}

func (f *FlushState) SetEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = enabled
}

func (f *FlushState) Toggle() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = !f.enabled
	return f.enabled
}

func (f *FlushState) TryLock() bool {
	return f.flushLock.TryLock()
}

func (f *FlushState) Unlock() {
	f.flushLock.Unlock()
}

func (f *FlushState) RecordFlush(count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushCount++
	f.lastFlushTime = time.Now()
}

func (f *FlushState) RecordFailure() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedAttempts++
}

func (f *FlushState) Stats() (count int64, lastTime time.Time, failures int64) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.flushCount, f.lastFlushTime, f.failedAttempts
}

func (s *Service) FlushConversation(ctx context.Context, userID, projectID string, turns []ConversationTurn, cfg FlushConfig) (*FlushResult, error) {
	start := time.Now()
	result := &FlushResult{}

	if !cfg.Enabled || len(turns) == 0 {
		return result, nil
	}

	windows := chunkTurns(turns, cfg.WindowSize)
	for _, window := range windows {
		content := extractWindowContent(window)

		if cfg.AutoExtract {
			items := s.extractFacts(content, userID, projectID, cfg.MinImportance)
			for _, item := range items {
				if err := s.Save(ctx, item); err == nil {
					result.Flushed++
				}
			}
			result.Extracted = len(items)
		} else {
			item := &memport.MemoryItem{
				UserID:     userID,
				ProjectID:  projectID,
				Scope:      memport.ScopeProject,
				Category:   "conversation_flush",
				Content:    content,
				Importance: cfg.MinImportance,
				Source:     "flush",
			}
			if err := s.Save(ctx, item); err == nil {
				result.Flushed++
			}
		}
	}

	result.Skipped = len(turns) - result.Flushed
	result.Duration = time.Since(start)
	return result, nil
}

func chunkTurns(turns []ConversationTurn, size int) [][]ConversationTurn {
	var windows [][]ConversationTurn
	for i := 0; i < len(turns); i += size {
		end := i + size
		if end > len(turns) {
			end = len(turns)
		}
		window := turns[i:end]
		hasUser := false
		for _, t := range window {
			if t.Role == "user" {
				hasUser = true
				break
			}
		}
		if hasUser {
			windows = append(windows, window)
		}
	}
	return windows
}

func extractWindowContent(window []ConversationTurn) string {
	var b strings.Builder
	for _, t := range window {
		if t.Role == "user" || t.Role == "assistant" {
			content := strings.TrimSpace(t.Content)
			if len(content) > 5 {
				b.WriteString(fmt.Sprintf("[%s] %s\n", t.Role, truncateStr(content, 500)))
			}
		}
	}
	return b.String()
}

func (s *Service) extractFacts(content, userID, projectID string, minImportance int) []*memport.MemoryItem {
	var items []*memport.MemoryItem

	if s.extractor != nil {
		return items
	}

	rules := []struct{ prefix, category string }{
		{"记住", "user_preference"},
		{"偏好", "user_preference"},
		{"以后", "user_preference"},
		{"项目", "project_convention"},
		{"架构", "project_architecture"},
		{"约定", "project_convention"},
		{"总是", "user_preference"},
	}

	for _, rule := range rules {
		if strings.Contains(content, rule.prefix) {
			items = append(items, &memport.MemoryItem{
				UserID:     userID,
				ProjectID:  projectID,
				Scope:      memport.ScopeProject,
				Category:   rule.category,
				Content:    truncateStr(content, 300),
				Importance: minImportance + 10,
				Source:     "flush_extract",
			})
			break
		}
	}

	return items
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
