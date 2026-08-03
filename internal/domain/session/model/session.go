package model

import "time"

type Session struct {
	ID           string
	UserID       string
	ProjectID    string
	AgentID      string
	Title        string
	Status       string
	MessageCount int
	TokenUsed    int
	WorkingDir   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewSession(id, userID, projectID, title, workDir string) *Session {
	now := time.Now()
	if title == "" {
		title = "new session"
	}
	return &Session{
		ID: id, UserID: userID, ProjectID: projectID,
		AgentID: "code-agent", Title: title, Status: "ACTIVE",
		WorkingDir: workDir, CreatedAt: now, UpdatedAt: now,
	}
}

func (s *Session) Touch() { s.UpdatedAt = time.Now() }

func (s *Session) AddTokens(n int) {
	if n > 0 {
		s.TokenUsed += n
	}
	s.MessageCount++
	s.Touch()
}

type Message struct {
	ID         string
	SessionID  string
	Role       string // user|assistant|tool|system
	Content    string
	ToolName   string
	ToolCallID string
	Step       int
	TokenCount int
	Priority   int
	CreatedAt  time.Time
}

func NewMessage(id, sessionID, role, content string) *Message {
	return &Message{
		ID: id, SessionID: sessionID, Role: role, Content: content,
		Priority: 1, CreatedAt: time.Now(),
	}
}
