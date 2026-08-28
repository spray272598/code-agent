package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	"github.com/spray272598/code-agent/internal/observability"
)

type CheckpointService struct {
	runs       *checkpoint.RunRegistry
	ckStore    checkpoint.Store
	perm       *security.Guard
	hooks      *hook.Bus
	redis      *redisx.Client
	timeoutSec int
	workspace  string
}

func (cs *CheckpointService) markRun(session *sessmodel.Session, req ChatRequest, status string, pending *security.PendingConfirm, errClass string) {
	if cs.ckStore == nil || session == nil {
		return
	}
	snap := &checkpoint.Snapshot{
		SessionID: session.ID, UserID: session.UserID, ProjectID: session.ProjectID,
		Status: status, Goal: req.Message, LastInput: req.Message, ErrorClass: errClass,
		UpdatedAt: time.Now(), CreatedAt: time.Now(),
	}
	if pending != nil {
		snap.Pending = &checkpoint.PendingTool{
			ID: pending.ID, SessionID: pending.SessionID, Tool: pending.Tool, Args: pending.Args,
			Reason: pending.Reason, RuleID: pending.RuleID, Layer: pending.Layer, CreatedAt: pending.CreatedAt,
		}
	}
	if prev, err := cs.ckStore.Get(context.Background(), session.ID); err != nil {
		observability.LogError("checkpoint get for markRun", err)
	} else if prev != nil && !prev.CreatedAt.IsZero() {
		snap.CreatedAt = prev.CreatedAt
	}
	if err := cs.ckStore.Save(context.Background(), snap); err != nil {
		observability.LogError("checkpoint save markRun", err)
	}
}

func (cs *CheckpointService) persistResultCheckpoint(session *sessmodel.Session, req ChatRequest, res *engine.Result, ctxErr error) {
	if cs.ckStore == nil || session == nil || res == nil {
		return
	}
	status := checkpoint.StatusCompleted
	if ctxErr != nil {
		status = checkpoint.StatusCancelled
		res.ErrorClass = "cancel"
	} else if res.NeedPermission {
		status = checkpoint.StatusInterrupt
	} else if res.ErrorClass != "" && res.ErrorClass != "permission" {
		status = checkpoint.StatusFailed
	}
	var pend *security.PendingConfirm
	if p, ok := res.Pending.(*security.PendingConfirm); ok {
		pend = p
	} else if res.NeedPermission && cs.perm != nil {
		if list := cs.perm.ListPending(session.ID); len(list) > 0 {
			pend = list[0]
		}
	}
	cs.markRun(session, req, status, pend, res.ErrorClass)
}

func (cs *CheckpointService) touchStep(ctx context.Context, sessionID string, step int, tool string) {
	if cs.ckStore == nil || sessionID == "" {
		return
	}
	snap, err := cs.ckStore.Get(ctx, sessionID)
	if err != nil || snap == nil {
		return
	}
	if snap.Status != checkpoint.StatusRunning {
		return
	}
	if step > snap.Step {
		snap.Step = step
	}
	if tool != "" {
		if snap.Meta == nil {
			snap.Meta = map[string]any{}
		}
		snap.Meta["lastTool"] = tool
		snap.Meta["lastToolAt"] = time.Now()
	}
	if err := cs.ckStore.Save(ctx, snap); err != nil {
		observability.LogError("step checkpoint save", err)
	}
}

func (cs *CheckpointService) SetHooks(h *hook.Bus) {
	cs.hooks = h
	if cs.hooks == nil || cs.ckStore == nil {
		return
	}
	cs.hooks.On(hook.PostToolUse, func(ctx context.Context, ev hook.Event) error {
		cs.touchStep(ctx, ev.SessionID, ev.Step, ev.Tool)
		return nil
	})
}

func (cs *CheckpointService) GetCheckpoint(ctx context.Context, sessionID string) (*checkpoint.Snapshot, error) {
	if cs.ckStore == nil {
		return nil, fmt.Errorf("checkpoint store disabled")
	}
	return cs.ckStore.Get(ctx, sessionID)
}

func (cs *CheckpointService) ListCheckpoints(ctx context.Context, status string, limit int) ([]*checkpoint.Snapshot, error) {
	if cs.ckStore == nil {
		return nil, fmt.Errorf("checkpoint store disabled")
	}
	return cs.ckStore.List(ctx, status, limit)
}

func (cs *CheckpointService) ListResumable(ctx context.Context) []*checkpoint.Snapshot {
	if cs.ckStore == nil {
		return nil
	}
	list, err := cs.ckStore.List(ctx, checkpoint.StatusRunning, 200)
	if err != nil {
		return nil
	}
	var out []*checkpoint.Snapshot
	for _, s := range list {
		if s == nil {
			continue
		}
		if cs.runs != nil && cs.runs.IsRunning(s.SessionID) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (cs *CheckpointService) RestoreCheckpoints(ctx context.Context) (int, error) {
	if cs.ckStore == nil || cs.perm == nil {
		return 0, nil
	}
	list, err := cs.ckStore.List(ctx, checkpoint.StatusInterrupt, 200)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range list {
		if s == nil || s.Pending == nil {
			continue
		}
		p := &security.PendingConfirm{
			ID: s.Pending.ID, SessionID: s.Pending.SessionID, Tool: s.Pending.Tool,
			Args: s.Pending.Args, Reason: s.Pending.Reason, RuleID: s.Pending.RuleID,
			Layer: s.Pending.Layer, CreatedAt: s.Pending.CreatedAt,
		}
		cs.perm.RestorePending(p)
		n++
	}
	return n, nil
}

func (cs *CheckpointService) IsSessionRunning(sessionID string) bool {
	if cs.runs == nil {
		return false
	}
	return cs.runs.IsRunning(sessionID)
}

func (cs *CheckpointService) ActiveRuns() []string {
	if cs.runs == nil {
		return nil
	}
	return cs.runs.Active()
}

func (cs *CheckpointService) SendControl(sessionID string, sig engine.ControlSignal, goal string) bool {
	if cs.runs == nil || sessionID == "" {
		return false
	}
	return cs.runs.Control(sessionID, sig, goal)
}

func (cs *CheckpointService) ReplanSession(sessionID, newGoal string) bool {
	sig := engine.ControlReplan
	if strings.TrimSpace(newGoal) != "" {
		sig = engine.ControlReplanWithGoal
	}
	return cs.SendControl(sessionID, sig, strings.TrimSpace(newGoal))
}

func (cs *CheckpointService) PauseSession(sessionID string) bool {
	return cs.SendControl(sessionID, engine.ControlPause, "")
}

func (cs *CheckpointService) ResumeControl(sessionID string) bool {
	return cs.SendControl(sessionID, engine.ControlResume, "")
}

func (cs *CheckpointService) InterruptSession(sessionID, reason string) (bool, error) {
	cs.SendControl(sessionID, engine.ControlInterrupt, "")
	return cs.CancelSession(sessionID, reason)
}

func (cs *CheckpointService) EnterPlanMode(sessionID string) bool {
	return cs.SendControl(sessionID, engine.ControlPlanExplore, "")
}

func (cs *CheckpointService) ExitPlanMode(sessionID string) bool {
	return cs.SendControl(sessionID, engine.ControlPlanImplement, "")
}

func (cs *CheckpointService) CancelSession(sessionID, reason string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("sessionId required")
	}
	ok := false
	if cs.runs != nil {
		ok = cs.runs.Cancel(sessionID)
	}
	if cs.ckStore != nil {
		snap := &checkpoint.Snapshot{
			SessionID: sessionID, Status: checkpoint.StatusCancelled,
			ErrorClass: "cancel", Meta: map[string]any{"reason": reason, "hadActive": ok},
			UpdatedAt: time.Now(), CreatedAt: time.Now(),
		}
		if prev, _ := cs.ckStore.Get(context.Background(), sessionID); prev != nil {
			snap.Goal = prev.Goal
			snap.LastInput = prev.LastInput
			snap.UserID = prev.UserID
			snap.CreatedAt = prev.CreatedAt
		}
		if err := cs.ckStore.Save(context.Background(), snap); err != nil {
			observability.LogError("checkpoint save cancel", err)
		}
	}
	return ok, nil
}

func (cs *CheckpointService) acquireRunLock(ctx context.Context, sessionID string) (func(), error) {
	if cs.redis == nil || !cs.redis.Enabled() || sessionID == "" {
		return func() {}, nil
	}
	val := newID("lock")
	ttl := time.Duration(cs.timeoutSec)*time.Second + 15*time.Second
	ok, err := cs.redis.TryLock(ctx, "run:lock:"+sessionID, val, ttl)
	if err != nil {
		return func() {}, nil
	}
	if !ok {
		return nil, fmt.Errorf("session %s is already running", sessionID)
	}
	return func() {
		if err := cs.redis.Unlock(context.Background(), "run:lock:"+sessionID, val); err != nil {
			slog.Warn("failed to release run lock", "session", sessionID, "error", err)
		}
	}, nil
}
