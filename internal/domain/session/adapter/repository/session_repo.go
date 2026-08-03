package repository

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/session/model"
)

type ISessionRepository interface {
	Save(ctx context.Context, s *model.Session) error
	FindByID(ctx context.Context, id string) (*model.Session, error)
	ListByUser(ctx context.Context, userID string, limit int) ([]*model.Session, error)
}

type IMessageRepository interface {
	Save(ctx context.Context, m *model.Message) error
	ListBySession(ctx context.Context, sessionID string, limit int) ([]*model.Message, error)
	ListAsMaps(ctx context.Context, sessionID string, limit int) ([]map[string]any, error)
}
