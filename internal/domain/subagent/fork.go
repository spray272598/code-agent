package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ForkMode defines how a subagent is forked from a parent.
type ForkMode string

const (
	ForkModeFresh    ForkMode = "fresh"    // fresh child with no history
	ForkModeContinue ForkMode = "continue" // continue from parent's last state
	ForkModeResume   ForkMode = "resume"   // resume from a specific checkpoint
)

// ForkConfig configures forking behavior.
type ForkConfig struct {
	Mode         ForkMode `json:"mode"`
	ResumeFrom   string   `json:"resume_from,omitempty"`   // checkpoint ID for resume mode
	MaxDepth     int      `json:"max_depth"`               // max nesting depth (0 = no limit)
	ParentDepth  int      `json:"parent_depth"`            // current depth of parent
	IncludeHistory bool   `json:"include_history"`         // include parent's conversation history
	MaxHistory   int      `json:"max_history"`             // max history entries to include (0 = all)
}

// ForkManager manages forking of subagents from parent sessions.
type ForkManager struct {
	mu          sync.RWMutex
	sessions    map[string]*ForkSessionHistory // sessionID -> history
	maxDepth    int
}

// ForkSessionHistory tracks a session's conversation history for forking.
type ForkSessionHistory struct {
	SessionID   string            `json:"session_id"`
	ParentID    string            `json:"parent_id,omitempty"`
	Messages    []ForkHistoryEntry `json:"messages"`
	Depth       int               `json:"depth"`
	CreatedAt   time.Time         `json:"created_at"`
	LastActive  time.Time         `json:"last_active"`
}

// ForkHistoryEntry is a single conversation entry for forking.
type ForkHistoryEntry struct {
	Role      string    `json:"role"` // system|user|assistant|tool
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// NewForkManager creates a new fork manager.
func NewForkManager(maxDepth int) *ForkManager {
	return &ForkManager{
		sessions: make(map[string]*ForkSessionHistory),
		maxDepth: maxDepth,
	}
}

// RegisterSession registers a session's history for potential forking.
func (fm *ForkManager) RegisterSession(sessionID, parentID string, depth int) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.sessions[sessionID] = &ForkSessionHistory{
		SessionID:  sessionID,
		ParentID:   parentID,
		Depth:      depth,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}
}

// AddMessage adds a message to a session's history.
func (fm *ForkManager) AddMessage(sessionID, role, content string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	h, ok := fm.sessions[sessionID]
	if !ok {
		return
	}
	h.Messages = append(h.Messages, ForkHistoryEntry{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})
	h.LastActive = time.Now()
}

// CanFork checks if a session can be forked.
func (fm *ForkManager) CanFork(parentSessionID string, config ForkConfig) (bool, string) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	parent, ok := fm.sessions[parentSessionID]
	if !ok {
		return false, fmt.Sprintf("parent session %s not found", parentSessionID)
	}

	if fm.maxDepth > 0 && parent.Depth >= fm.maxDepth {
		return false, fmt.Sprintf("max fork depth %d reached (current: %d)", fm.maxDepth, parent.Depth)
	}

	if config.Mode == ForkModeResume && config.ResumeFrom == "" {
		return false, "resume mode requires a checkpoint ID"
	}

	return true, ""
}

// Fork creates a new session from a parent's history.
func (fm *ForkManager) Fork(ctx context.Context, parentSessionID string, config ForkConfig, _ SubagentRunner) (*SubagentHandle, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	parent, ok := fm.sessions[parentSessionID]
	if !ok {
		return nil, fmt.Errorf("parent session %s not found", parentSessionID)
	}

	if fm.maxDepth > 0 && parent.Depth >= fm.maxDepth {
		return nil, fmt.Errorf("max fork depth %d reached", fm.maxDepth)
	}

	// Build forked history
	var messages []ForkHistoryEntry
	if config.IncludeHistory {
		messages = make([]ForkHistoryEntry, len(parent.Messages))
		copy(messages, parent.Messages)
		if config.MaxHistory > 0 && len(messages) > config.MaxHistory {
			messages = messages[len(messages)-config.MaxHistory:]
		}
	}

	// Create new session
	newSessionID := fmt.Sprintf("fork-%s-%d", parentSessionID, time.Now().UnixNano()%1e9)
	forked := &ForkSessionHistory{
		SessionID:  newSessionID,
		ParentID:   parentSessionID,
		Messages:   messages,
		Depth:      parent.Depth + 1,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}
	fm.sessions[newSessionID] = forked

	// Build prompt from history
	prompt := buildForkPrompt(messages, config)

	// Create subagent handle
	handle := &SubagentHandle{
		TaskID:        newSessionID,
		Status:        SubagentStatusPending,
		Description:   fmt.Sprintf("forked from %s (depth %d)", parentSessionID, forked.Depth),
		SubagentType:  "general",
		StartedAt:     time.Now(),
		ParentSession: newSessionID,
	}

	_ = prompt // prompt would be used by the actual runner

	return handle, nil
}

// buildForkPrompt constructs a prompt from forked history.
func buildForkPrompt(messages []ForkHistoryEntry, config ForkConfig) string {
	if len(messages) == 0 {
		return "Continue the task from the parent session."
	}

	var b strings.Builder
	b.WriteString("## Forked Session Context\n\n")
	b.WriteString("This session was forked from a parent session. Here is the conversation history:\n\n")

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			b.WriteString(fmt.Sprintf("User: %s\n\n", msg.Content))
		case "assistant":
			b.WriteString(fmt.Sprintf("Assistant: %s\n\n", msg.Content))
		case "tool":
			b.WriteString(fmt.Sprintf("Tool Result: %s\n\n", msg.Content))
		}
	}

	if config.Mode == ForkModeResume {
		b.WriteString(fmt.Sprintf("\nResume from checkpoint: %s\n", config.ResumeFrom))
	} else {
		b.WriteString("\nContinue the task from where the parent session left off.\n")
	}

	return b.String()
}

// GetHistory returns a session's history (for inspection).
func (fm *ForkManager) GetHistory(sessionID string) (*ForkSessionHistory, bool) {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	h, ok := fm.sessions[sessionID]
	return h, ok
}

// RemoveSession removes a session from the fork manager.
func (fm *ForkManager) RemoveSession(sessionID string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	delete(fm.sessions, sessionID)
}

// SessionCount returns the number of tracked sessions.
func (fm *ForkManager) SessionCount() int {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	return len(fm.sessions)
}
