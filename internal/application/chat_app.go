package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/auth"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/domain/hook"
	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
	mcpmodel "github.com/spray272598/code-agent/internal/domain/mcp/model"
	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/slash"
	sshmodel "github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
)

type ChatApp struct {
	loop        engine.Runner
	sessions    sessrepo.ISessionRepository
	messages    sessrepo.IMessageRepository
	tools       *tool.MapRegistry
	perm        *security.Guard
	redis       *redisx.Client
	skills      *skill.Service
	slash       *slash.Registry
	memSvc      *memory.Service
	auditRepo   audit.Repository
	blobs       blob.Store
	summaryRepo sessrepo.ISummaryRepository
	workspace   string
	keys        *auth.KeyStore
	mcpFactory  mcpport.IMCPManagerFactory

	// Extracted services
	cp      *CheckpointService
	idemSvc *IdempotencyService
	rateSvc *RateQuotaService
	sshSvc  *SSHService
	mcpFace *MCPFacade
}

// Set* methods retained for gradual migration; prefer application.Option.
func (a *ChatApp) SetSkills(s *skill.Service)  { a.skills = s }
func (a *ChatApp) SetMemory(s *memory.Service) { a.memSvc = s }
func (a *ChatApp) SetAudit(r audit.Repository) { a.auditRepo = r }
func (a *ChatApp) SetBlobStore(s blob.Store)   { a.blobs = s }

// MCP delegation methods
func (a *ChatApp) SetMCPFactory(f mcpport.IMCPManagerFactory) {
	a.mcpFactory = f
	a.mcpFace = &MCPFacade{factory: f}
}
func (a *ChatApp) MCPFactory() mcpport.IMCPManagerFactory { return a.mcpFactory }
func (a *ChatApp) MCPFor(ctx context.Context) (mcpport.IMCPManagerPort, error) {
	if a.mcpFace == nil {
		return nil, fmt.Errorf("mcp factory not configured")
	}
	return a.mcpFace.MCPFor(ctx)
}

// SSH delegation methods
func (a *ChatApp) SetSSH(pool *sshinfra.Pool, repo sshport.IConnectionRepository) {
	a.sshSvc = &SSHService{pool: pool, repo: repo}
}
func (a *ChatApp) SSHPool() *sshinfra.Pool {
	if a.sshSvc == nil {
		return nil
	}
	return a.sshSvc.pool
}
func (a *ChatApp) InstallSSH(ctx context.Context, cfg sshmodel.ConnectionConfig) error {
	if a.sshSvc == nil {
		return fmt.Errorf("ssh disabled")
	}
	return a.sshSvc.InstallSSH(ctx, cfg)
}
func (a *ChatApp) ListSSHConnections(ctx context.Context) ([]*sshmodel.ConnectionConfig, error) {
	if a.sshSvc == nil {
		return nil, fmt.Errorf("ssh disabled")
	}
	return a.sshSvc.ListSSHConnections(ctx)
}
func (a *ChatApp) DeleteSSHConnection(ctx context.Context, id string) error {
	if a.sshSvc == nil {
		return fmt.Errorf("ssh disabled")
	}
	return a.sshSvc.DeleteSSHConnection(ctx, id)
}
func (a *ChatApp) SSHHealth() []sshmodel.HealthStatus {
	if a.sshSvc == nil {
		return nil
	}
	return a.sshSvc.SSHHealth()
}
func (a *ChatApp) Slash() *slash.Registry    { return a.slash }
func (a *ChatApp) Skills() *skill.Service    { return a.skills }
func (a *ChatApp) Memory() *memory.Service   { return a.memSvc }
func (a *ChatApp) Audit() audit.Repository   { return a.auditRepo }
func (a *ChatApp) Blobs() blob.Store         { return a.blobs }

func (a *ChatApp) GetBlob(ctx context.Context, key string) ([]byte, error) {
	if a.blobs == nil {
		return nil, fmt.Errorf("blob store disabled")
	}
	return a.blobs.Get(ctx, key)
}

func (a *ChatApp) ListAudit(ctx context.Context, userID, sessionID string, limit int) ([]audit.Entry, error) {
	if a.auditRepo == nil {
		return nil, nil
	}
	return a.auditRepo.ListBySession(ctx, userID, sessionID, limit)
}

// ListAuditCtx is the ctx-driven form. In the single-operator harness there is
// no tenant, so it delegates to the audit repo's ListForUser which matches every
// actor for the given session.
func (a *ChatApp) ListAuditCtx(ctx context.Context, sessionID string, limit int) ([]audit.Entry, error) {
	if a.auditRepo == nil {
		return nil, nil
	}
	return a.auditRepo.ListForUser(ctx, sessionID, limit)
}

// SaveMemory API/helper
func (a *ChatApp) SaveMemory(ctx context.Context, item *memport.MemoryItem) error {
	if a.memSvc == nil {
		return fmt.Errorf("memory disabled")
	}
	return a.memSvc.Save(ctx, item)
}

func (a *ChatApp) ListMemory(ctx context.Context, projectID, scope string, limit int) ([]memport.MemoryItem, error) {
	if a.memSvc == nil {
		return nil, nil
	}
	return a.memSvc.List(ctx, projectID, memport.Scope(scope), limit)
}

func (a *ChatApp) SearchMemory(ctx context.Context, projectID, query string, limit int) ([]memport.MemoryItem, error) {
	if a.memSvc == nil {
		return nil, nil
	}
	return a.memSvc.Search(ctx, projectID, query, limit)
}

// InstallMCP installs/updates an MCP server via domain port.
func (a *ChatApp) InstallMCP(ctx context.Context, name, transport, command string, args []string, env map[string]string, url string, enabled bool, timeout int) error {
	if a.mcpFactory == nil {
		return fmt.Errorf("mcp disabled")
	}
	mgr, err := a.mcpFactory.For(ctx)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	if transport == "" {
		transport = "stdio"
	}
	if timeout <= 0 {
		timeout = 60
	}
	return mgr.AddOrUpdate(ctx, mcpmodel.ServerConfig{
		Name: name, Transport: transport, Command: command, Args: args,
		Env: env, URL: url, Enabled: enabled, TimeoutSec: timeout,
	})
}

// Auth verifies API key via SHA-256 hash + constant-time compare (never stores plaintext after boot).
func (a *ChatApp) Auth(apiKey string) bool {
	if a.keys == nil || a.keys.Empty() {
		return true // dev open when no keys configured
	}
	return a.keys.Valid(apiKey)
}

func (a *ChatApp) CreateSession(userID, projectID, title string) (*sessmodel.Session, error) {
	if userID == "" {
		userID = "anonymous"
	}
	s := sessmodel.NewSession(newID("sess"), userID, projectID, title, a.workspace)
	if err := a.sessions.Save(context.Background(), s); err != nil {
		return nil, err
	}
	return s, nil
}

func (a *ChatApp) GetSession(id string) (*sessmodel.Session, error) {
	return a.sessions.FindByID(context.Background(), id)
}

func (a *ChatApp) ListSessions(userID string) ([]*sessmodel.Session, error) {
	return a.sessions.ListByUser(context.Background(), userID, 50)
}

func (a *ChatApp) ListTools() []map[string]string {
	return a.tools.Descriptions()
}

func (a *ChatApp) Permission() *security.Guard { return a.perm }

type ChatRequest struct {
	SessionID      string `json:"sessionId"`
	UserID         string `json:"userId"`
	ProjectID      string `json:"projectId"`
	Message        string `json:"message"`
	AutoApprove    bool   `json:"autoApprove"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

type ChatResponse struct {
	SessionID      string `json:"sessionId"`
	Response       string `json:"response"`
	Steps          int    `json:"steps"`
	ToolCalls      int    `json:"toolCalls"`
	TokenUsed      int    `json:"tokenUsed"`
	NeedPermission bool   `json:"needPermission,omitempty"`
	Pending        any    `json:"pendingPermission,omitempty"`
	ErrorClass     string `json:"errorClass,omitempty"`
	Slash          bool   `json:"slash,omitempty"`
}

// Checkpoint delegation
func (a *ChatApp) SetHooks(h *hook.Bus) { a.cp.SetHooks(h) }
func (a *ChatApp) GetCheckpoint(ctx context.Context, sid string) (*checkpoint.Snapshot, error) {
	return a.cp.GetCheckpoint(ctx, sid)
}
func (a *ChatApp) ListCheckpoints(ctx context.Context, status string, limit int) ([]*checkpoint.Snapshot, error) {
	return a.cp.ListCheckpoints(ctx, status, limit)
}
func (a *ChatApp) IsSessionRunning(sid string) bool { return a.cp.IsSessionRunning(sid) }
func (a *ChatApp) ActiveRuns() []string             { return a.cp.ActiveRuns() }
func (a *ChatApp) SendControl(sid string, sig engine.ControlSignal, goal string) bool {
	return a.cp.SendControl(sid, sig, goal)
}
func (a *ChatApp) ReplanSession(sid, newGoal string) bool { return a.cp.ReplanSession(sid, newGoal) }
func (a *ChatApp) PauseSession(sid string) bool           { return a.cp.PauseSession(sid) }
func (a *ChatApp) ResumeControl(sid string) bool          { return a.cp.ResumeControl(sid) }
func (a *ChatApp) InterruptSession(sid, reason string) (bool, error) {
	return a.cp.InterruptSession(sid, reason)
}
func (a *ChatApp) EnterPlanMode(sid string) bool { return a.cp.EnterPlanMode(sid) }
func (a *ChatApp) ExitPlanMode(sid string) bool  { return a.cp.ExitPlanMode(sid) }
func (a *ChatApp) CancelSession(sid, reason string) (bool, error) {
	return a.cp.CancelSession(sid, reason)
}
func (a *ChatApp) ListResumable(ctx context.Context) []*checkpoint.Snapshot {
	return a.cp.ListResumable(ctx)
}
func (a *ChatApp) RestoreCheckpoints(ctx context.Context) (int, error) {
	return a.cp.RestoreCheckpoints(ctx)
}

// ResumeSession continues an interrupted run, injecting step/tool context so the
// agent can pick up near the break point instead of redoing completed steps.
func (a *ChatApp) ResumeSession(ctx context.Context, sessionID, message string) (*ChatResponse, error) {
	snap, err := a.GetCheckpoint(ctx, sessionID)
	if err != nil || snap == nil {
		return nil, fmt.Errorf("no resumable checkpoint for session %s", sessionID)
	}
	hint := ""
	if snap.Step > 0 {
		hint += fmt.Sprintf("Last interrupted at step %d", snap.Step)
	}
	if t, ok := snap.Meta["lastTool"]; ok {
		if hint != "" {
			hint += ", "
		}
		hint += fmt.Sprintf("last executed tool: %v", t)
	}
	if hint != "" {
		hint += "."
	}
	if strings.TrimSpace(message) == "" {
		message = "Continue."
	}
	if hint != "" {
		message = message + "\n[Checkpoint context] " + hint + " Please continue unfinished work from the checkpoint, do not repeat completed steps."
	}
	req := ChatRequest{
		SessionID: sessionID, UserID: snap.UserID, ProjectID: snap.ProjectID,
		Message: message,
	}
	return a.Chat(req)
}

func (a *ChatApp) markRun(session *sessmodel.Session, req ChatRequest, status string, pending *security.PendingConfirm, errClass string) {
	a.cp.markRun(session, req, status, pending, errClass)
}

func (a *ChatApp) persistResultCheckpoint(session *sessmodel.Session, req ChatRequest, res *engine.Result, ctxErr error) {
	a.cp.persistResultCheckpoint(session, req, res, ctxErr)
}

func (a *ChatApp) resolveSession(req ChatRequest) (*sessmodel.Session, error) {
	if req.SessionID != "" {
		s, err := a.sessions.FindByID(context.Background(), req.SessionID)
		if err != nil {
			return nil, err
		}
		if s != nil && s.Status == "ACTIVE" {
			return s, nil
		}
	}
	userID := req.UserID
	if userID == "" {
		userID = "anonymous"
	}
	s := sessmodel.NewSession(newID("sess"), userID, req.ProjectID, "auto", a.workspace)
	if err := a.sessions.Save(context.Background(), s); err != nil {
		return nil, err
	}
	return s, nil
}

// Rate/quota delegation
func (a *ChatApp) checkRate(ctx context.Context, userID string) error {
	return a.rateSvc.checkRate(ctx, userID)
}
func (a *ChatApp) checkQuota(ctx context.Context, userID string) error {
	return a.rateSvc.checkQuota(ctx, userID)
}
func (a *ChatApp) UsageSnapshot(ctx context.Context, userID, sessionID string) *Usage {
	return a.rateSvc.UsageSnapshot(ctx, userID, sessionID)
}

func newID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}
