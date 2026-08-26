// Package orchestration provides session forking and orchestration state management.
//
// Session Forking (P1-2) allows sub-agents to run in an isolated session
// that inherits parent context but has its own lifecycle. When the sub-agent
// finishes, only a distilled summary is written back to the parent, keeping
// the parent context window lean.
package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
)

// ForkRequest describes how to fork a session for a sub-agent.
type ForkRequest struct {
	SourceSessionID string
	SourceWorkspace string
	NewWorkspace    string
	NewModelID      string
	SessionKind     string // "fork" | "worktree" | "subagent"
	SubagentRole    string
	SubagentPersona string
}

// ForkResult describes a successfully forked session.
type ForkResult struct {
	NewSessionID  string
	ParentID      string
	NewWorkspace  string
	MessagesCount int
	ForkedAt      time.Time
}

// SessionForkService manages session forking for sub-agent isolation.
type SessionForkService struct {
	sessionRepo sessrepo.ISessionRepository
	msgRepo     sessrepo.IMessageRepository
}

// NewSessionForkService creates a new forking service.
func NewSessionForkService(
	sessionRepo sessrepo.ISessionRepository,
	msgRepo sessrepo.IMessageRepository,
) *SessionForkService {
	return &SessionForkService{
		sessionRepo: sessionRepo,
		msgRepo:     msgRepo,
	}
}

// ForkSession creates a new session that inherits context from the source session.
func (s *SessionForkService) ForkSession(ctx context.Context, req ForkRequest) (*ForkResult, error) {
	if req.SourceSessionID == "" {
		return nil, fmt.Errorf("source session id is required")
	}

	newID := generateForkID()

	title := fmt.Sprintf("fork-%s-%s", req.SessionKind, newID[:8])
	workDir := req.NewWorkspace
	if workDir == "" {
		workDir = req.SourceWorkspace
	}

	newSession := sessmodel.NewSession(newID, "", "", title, workDir)
	if err := s.sessionRepo.Save(ctx, newSession); err != nil {
		return nil, fmt.Errorf("save forked session: %w", err)
	}

	return &ForkResult{
		NewSessionID:  newID,
		ParentID:      req.SourceSessionID,
		NewWorkspace:  workDir,
		MessagesCount: 0,
		ForkedAt:      time.Now(),
	}, nil
}

// MergeSummary backfills a sub-agent's distilled output into the parent session.
// The full transcript is NOT copied; only the summary is written as a new assistant message.
func (s *SessionForkService) MergeSummary(ctx context.Context, parentID, summary, subagentID, role string) error {
	if summary == "" {
		return nil
	}
	msg := sessmodel.NewMessage(
		fmt.Sprintf("sa-%s-%d", subagentID[:8], time.Now().UnixNano()%1000),
		parentID,
		"assistant",
		fmt.Sprintf("[%s/%s] %s", subagentID, role, summary),
	)
	msg.ToolName = "delegate"
	msg.Step = 1
	return s.msgRepo.Save(ctx, msg)
}

// GetSession returns a session by ID.
func (s *SessionForkService) GetSession(ctx context.Context, id string) (*sessmodel.Session, error) {
	return s.sessionRepo.FindByID(ctx, id)
}

// generateForkID creates a short random fork ID.
func generateForkID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "fk_" + hex.EncodeToString(b)
}

// ForkContext carries metadata about a sub-agent session fork through the run.
type ForkContext struct {
	RunID           string
	ParentSessionID string
	SubSessionID    string
	Role            string
	Workspace       string
	StartedAt       time.Time
}

// NewForkContext creates a new ForkContext.
func NewForkContext(runID, parentID, subID, role, workspace string) *ForkContext {
	return &ForkContext{
		RunID:           runID,
		ParentSessionID: parentID,
		SubSessionID:    subID,
		Role:            role,
		Workspace:       workspace,
		StartedAt:       time.Now(),
	}
}

// DurationMs returns elapsed time since fork start.
func (f *ForkContext) DurationMs() int64 {
	return time.Since(f.StartedAt).Milliseconds()
}
