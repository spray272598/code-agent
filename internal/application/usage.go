package application

import (
	"context"
	"fmt"
	"time"

	"github.com/spray272598/code-agent/internal/observability"
)

// Usage is a point-in-time view of resource consumption, surfaced via the
// `/status` slash command and the `GET /api/v1/usage` endpoint. It directly
// reuses observability.CounterRegistry (token/quota counters) and the per-user
// daily token counters (3.3) tracked in Redis.
type Usage struct {
	GeneratedAt   string         `json:"generated_at"`
	QuotaEnabled  bool           `json:"quota_enabled"`
	QuotaPerDay   int            `json:"quota_per_day"`
	DailyUsed     int            `json:"daily_used"`
	DailyRemaining int           `json:"daily_remaining"`
	SessionUsed   int            `json:"session_used"`
	ActiveRuns    int            `json:"active_runs"`
	Counters      map[string]any `json:"counters"`
}

// UsageSnapshot returns the current usage view for a user/session.
func (a *ChatApp) UsageSnapshot(ctx context.Context, userID, sessionID string) *Usage {
	u := &Usage{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		QuotaEnabled: a.quotaEnabled,
		QuotaPerDay:  a.quotaPerDay,
		Counters:     observability.Current().Snapshot(),
	}
	if a.quotaEnabled && a.redis != nil && a.redis.Enabled() {
		if v, err := a.redis.Get(ctx, "token:user:"+userID+":"+todayKey()); err == nil && v != "" {
			var n int
			fmt.Sscanf(v, "%d", &n)
			u.DailyUsed = n
		}
		if a.quotaPerDay > 0 {
			u.DailyRemaining = a.quotaPerDay - u.DailyUsed
			if u.DailyRemaining < 0 {
				u.DailyRemaining = 0
			}
		}
	}
	if sessionID != "" && a.redis != nil && a.redis.Enabled() {
		if v, err := a.redis.Get(ctx, "token:sess:"+sessionID); err == nil && v != "" {
			var n int
			fmt.Sscanf(v, "%d", &n)
			u.SessionUsed = n
		}
	}
	if a.runs != nil {
		u.ActiveRuns = len(a.runs.ActiveIDs())
	}
	return u
}

// Render returns a human-readable status block for the TUI/CLI.
func (u *Usage) Render() string {
	remaining := "unlimited"
	if u.QuotaEnabled && u.QuotaPerDay > 0 {
		remaining = fmt.Sprintf("%d tokens", u.DailyRemaining)
	}
	return fmt.Sprintf(
		"Usage @ %s\n  daily used:    %d\n  daily quota:   %s\n  session used:  %d\n  active runs:   %d\n  tool calls:    %v\n  tokens total:  %v\n  quota denies:  %v\n  reflects:      %v\n  compressions:  %v",
		u.GeneratedAt, u.DailyUsed, remaining, u.SessionUsed, u.ActiveRuns,
		u.Counters["tool_calls"], u.Counters["tokens_total"], u.Counters["quota_deny_total"],
		u.Counters["reflect_total"], u.Counters["compress_total"],
	)
}
