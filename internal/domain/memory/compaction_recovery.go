package memory

import (
	"context"
	"time"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

type CompactionRecovery struct {
	memSvc      *Service
	checkpoints *DurableCheckpointStore
	maxLookback int
	minScore    float64
}

func NewCompactionRecovery(memSvc *Service, checkpoints *DurableCheckpointStore) *CompactionRecovery {
	return &CompactionRecovery{
		memSvc:      memSvc,
		checkpoints: checkpoints,
		maxLookback: 5,
		minScore:    0.1,
	}
}

type RecoveryResult struct {
	RecoveredItems int
	RecoveredText  string
	CheckpointHits int
	Duration       time.Duration
}

func (r *CompactionRecovery) AfterCompaction(ctx context.Context, userID, projectID, query string) (*RecoveryResult, error) {
	start := time.Now()
	result := &RecoveryResult{}

	if r.memSvc == nil {
		return result, nil
	}

	opts := DefaultSearchOptions()
	opts.MinScore = r.minScore
	scored, err := r.memSvc.HybridSearch(ctx, userID, projectID, query, opts)
	if err != nil {
		return result, err
	}

	recoveredIDs := make(map[int64]bool)
	var items []memport.MemoryItem
	for _, sc := range scored {
		items = append(items, sc.Item)
		recoveredIDs[sc.Item.ID] = true
	}
	result.RecoveredItems = len(items)

	if r.checkpoints != nil {
		sessions, _ := r.checkpoints.Sessions(ctx)
		checkpointHits := 0
		for _, sid := range sessions {
			entries, _ := r.checkpoints.List(ctx, sid)
			for _, e := range entries {
				if e.UserID == userID && len(recoveredIDs) < r.maxLookback*2 {
					checkpointHits++
				}
			}
			if checkpointHits >= r.maxLookback {
				break
			}
		}
		result.CheckpointHits = checkpointHits
	}

	if len(items) > 0 {
		result.RecoveredText = r.memSvc.FormatForPromptExtended(ctx, userID, projectID, query, len(items), opts)
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (r *CompactionRecovery) RecoverSessionContext(ctx context.Context, userID, projectID, sessionID, query string) ([]memport.MemoryItem, error) {
	if r.checkpoints == nil {
		return nil, nil
	}

	entries, err := r.checkpoints.List(ctx, sessionID)
	if err != nil || len(entries) == 0 {
		return nil, nil
	}

	var items []memport.MemoryItem
	for _, e := range entries {
		items = append(items, memport.MemoryItem{
			UserID: userID, ProjectID: projectID,
			Content: e.Content, Category: e.Category,
			Importance: 60, Scope: memport.ScopeProject,
			Source: "compaction_recovery",
		})
	}
	return items, nil
}
