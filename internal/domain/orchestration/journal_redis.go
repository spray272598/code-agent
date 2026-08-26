package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisJournalStorage persists journal entries to Redis using sorted sets.
// Entries are stored as JSON strings scored by timestamp for ordered retrieval.
// Suitable for high-throughput, low-latency orchestration logging.
//
// Key structure:
//
//	journal:{runID} → SortedSet (score=timestamp_unix_nano, value=JSON_entry)
//	journal:{runID}:state → Hash with current state snapshot
type RedisJournalStorage struct {
	client *redis.Client
	prefix string
	ctx    context.Context
}

// NewRedisJournalStorage creates a Redis-backed journal storage.
// addr examples: "localhost:6379"
func NewRedisJournalStorage(addr, password string, db int) (*RedisJournalStorage, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &RedisJournalStorage{
		client: client,
		prefix: "journal:",
		ctx:    ctx,
	}, nil
}

func (s *RedisJournalStorage) entryKey(runID string) string {
	return s.prefix + runID
}

func (s *RedisJournalStorage) stateKey(runID string) string {
	return s.prefix + runID + ":state"
}

func (s *RedisJournalStorage) Append(entry JournalEntry) error {
	entry.Timestamp = time.Now()
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	score := float64(entry.Timestamp.UnixNano())
	member := string(b)
	if err := s.client.ZAdd(s.ctx, s.entryKey(entry.RunID), redis.Z{
		Score:  score,
		Member: member,
	}).Err(); err != nil {
		return fmt.Errorf("zadd journal entry: %w", err)
	}
	// Trim old entries (keep last 1000 per run).
	s.client.ZRemRangeByRank(s.ctx, s.entryKey(entry.RunID), 0, -1001)
	return nil
}

func (s *RedisJournalStorage) ReadAll(runID string) ([]JournalEntry, error) {
	key := s.entryKey(runID)
	results, err := s.client.ZRangeByScore(s.ctx, key, &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("zrange journal: %w", err)
	}
	var entries []JournalEntry
	for _, raw := range results {
		var e JournalEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// SaveState persists the current JournalState as a Redis hash (O(1) lookup).
func (s *RedisJournalStorage) SaveState(state *JournalState) error {
	key := s.stateKey(state.RunID)
	s.mustCtx()
	pipe := s.client.TxPipeline()
	pipe.HSet(s.ctx, key, map[string]interface{}{
		"status":       string(state.Status),
		"goal":         state.Goal,
		"agent_budget": state.AgentBudget,
		"agents_used":  state.AgentsUsed,
		"tokens_used":  state.TokensUsed,
		"phases_done": func() string {
			b, _ := json.Marshal(state.PhasesDone)
			return string(b)
		}(),
		"updated_at": state.UpdatedAt.Format(time.RFC3339Nano),
	})
	pipe.Expire(s.ctx, key, 7*24*time.Hour)
	_, err := pipe.Exec(s.ctx)
	if err != nil {
		return fmt.Errorf("save journal state: %w", err)
	}
	return nil
}

// LoadState retrieves the current JournalState from Redis hash.
func (s *RedisJournalStorage) LoadState(runID string) (*JournalState, error) {
	key := s.stateKey(runID)
	h, err := s.client.HGetAll(s.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall journal state: %w", err)
	}
	if len(h) == 0 {
		return nil, nil
	}
	state := &JournalState{RunID: runID}
	if v, ok := h["status"]; ok {
		state.Status = StatusFromString(v)
	}
	if v, ok := h["goal"]; ok {
		state.Goal = v
	}
	if v, ok := h["agent_budget"]; ok {
		fmt.Sscanf(v, "%d", &state.AgentBudget)
	}
	if v, ok := h["agents_used"]; ok {
		fmt.Sscanf(v, "%d", &state.AgentsUsed)
	}
	if v, ok := h["tokens_used"]; ok {
		fmt.Sscanf(v, "%d", &state.TokensUsed)
	}
	if v, ok := h["phases_done"]; ok {
		json.Unmarshal([]byte(v), &state.PhasesDone)
	}
	if v, ok := h["updated_at"]; ok {
		state.UpdatedAt, _ = time.Parse(time.RFC3339Nano, v)
	}
	if state.AgentBudget == 0 {
		state.AgentBudget = 4
	}
	return state, nil
}

func (s *RedisJournalStorage) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *RedisJournalStorage) mustCtx() {
	if s.ctx == nil {
		s.ctx = context.Background()
	}
}

var _ JournalStorage = (*RedisJournalStorage)(nil)
