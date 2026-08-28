package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spray272598/code-agent/internal/domain/checkpoint"
	"github.com/spray272598/code-agent/internal/infrastructure/redisx"
	"github.com/spray272598/code-agent/internal/observability"
)

type RateQuotaService struct {
	redis        *redisx.Client
	rateEnabled  bool
	ratePerMin   int
	quotaEnabled bool
	quotaPerDay  int
	runs         *checkpoint.RunRegistry
}

func (rqs *RateQuotaService) checkRate(ctx context.Context, userID string) error {
	if !rqs.rateEnabled {
		return nil
	}
	if userID == "" {
		userID = "anonymous"
	}
	limit := rqs.ratePerMin
	if limit <= 0 {
		limit = 60
	}
	if rqs.redis == nil {
		return nil
	}
	ok, err := rqs.redis.AllowRate(ctx, "rl:chat:"+userID, limit, time.Minute)
	if err != nil {
		slog.Default().Warn("rate limit check failed, allowing request",
			"user_id", userID,
			"error", err,
		)
		return nil
	}
	if !ok {
		return fmt.Errorf("rate limit exceeded")
	}
	return nil
}

func (rqs *RateQuotaService) checkQuota(ctx context.Context, userID string) error {
	if !rqs.quotaEnabled || rqs.redis == nil {
		return nil
	}
	if userID == "" {
		userID = "anonymous"
	}
	used, err := rqs.redis.Get(ctx, "token:user:"+userID+":"+todayKey())
	if err != nil {
		slog.Default().Warn("quota check failed, allowing request",
			"user_id", userID,
			"error", err,
		)
		return nil
	}
	if used == "" {
		return nil
	}
	var usedTok int
	fmt.Sscanf(used, "%d", &usedTok)
	if quotaExceeded(usedTok, rqs.quotaPerDay) {
		observability.Current().AddQuotaDeny(1)
		return fmt.Errorf("daily token quota (%d) exhausted", rqs.quotaPerDay)
	}
	return nil
}

func quotaExceeded(used, quota int) bool {
	if quota <= 0 {
		return false
	}
	return used >= quota
}

func todayKey() string {
	return time.Now().UTC().Format("2006-01-02")
}

func (rqs *RateQuotaService) UsageSnapshot(ctx context.Context, userID, sessionID string) *Usage {
	u := &Usage{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		QuotaEnabled: rqs.quotaEnabled,
		QuotaPerDay:  rqs.quotaPerDay,
		Counters:     observability.Current().Snapshot(),
	}
	if rqs.quotaEnabled && rqs.redis != nil && rqs.redis.Enabled() {
		if v, err := rqs.redis.Get(ctx, "token:user:"+userID+":"+todayKey()); err == nil && v != "" {
			var n int
			fmt.Sscanf(v, "%d", &n)
			u.DailyUsed = n
		}
		if rqs.quotaPerDay > 0 {
			u.DailyRemaining = rqs.quotaPerDay - u.DailyUsed
			if u.DailyRemaining < 0 {
				u.DailyRemaining = 0
			}
		}
	}
	if sessionID != "" && rqs.redis != nil && rqs.redis.Enabled() {
		if v, err := rqs.redis.Get(ctx, "token:sess:"+sessionID); err == nil && v != "" {
			var n int
			fmt.Sscanf(v, "%d", &n)
			u.SessionUsed = n
		}
	}
	if rqs.runs != nil {
		u.ActiveRuns = len(rqs.runs.ActiveIDs())
	}
	return u
}
