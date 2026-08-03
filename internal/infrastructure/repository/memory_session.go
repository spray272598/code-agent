package repository

import (
	"context"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/session/model"
)

type MemorySessionRepo struct {
	mu   sync.RWMutex
	data map[string]*model.Session
}

func NewMemorySessionRepo() *MemorySessionRepo {
	return &MemorySessionRepo{data: map[string]*model.Session{}}
}

func (r *MemorySessionRepo) Save(_ context.Context, s *model.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.data[s.ID] = &cp
	return nil
}

func (r *MemorySessionRepo) FindByID(_ context.Context, id string) (*model.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.data[id]
	if s == nil {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (r *MemorySessionRepo) ListByUser(_ context.Context, userID string, limit int) ([]*model.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	var out []*model.Session
	for _, s := range r.data {
		if s.UserID == userID {
			cp := *s
			out = append(out, &cp)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type MemoryMessageRepo struct {
	mu   sync.RWMutex
	data map[string][]*model.Message
}

func NewMemoryMessageRepo() *MemoryMessageRepo {
	return &MemoryMessageRepo{data: map[string][]*model.Message{}}
}

func (r *MemoryMessageRepo) Save(_ context.Context, m *model.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *m
	r.data[m.SessionID] = append(r.data[m.SessionID], &cp)
	return nil
}

func (r *MemoryMessageRepo) ListBySession(_ context.Context, sessionID string, limit int) ([]*model.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := r.data[sessionID]
	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}
	out := make([]*model.Message, len(list))
	for i, m := range list {
		cp := *m
		out[i] = &cp
	}
	return out, nil
}

func (r *MemoryMessageRepo) ListAsMaps(ctx context.Context, sessionID string, limit int) ([]map[string]any, error) {
	list, err := r.ListBySession(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, map[string]any{
			"id": m.ID, "role": m.Role, "content": m.Content,
			"toolName": m.ToolName, "toolCallId": m.ToolCallID,
			"step": m.Step, "priority": m.Priority,
		})
	}
	return out, nil
}
