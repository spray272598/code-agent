package port

import "context"

// Scope of long-term memory.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// MemoryItem is a durable memory unit.
type MemoryItem struct {
	ID         int64
	UserID     string
	ProjectID  string
	Scope      Scope
	Category   string
	Content    string
	Importance int
	Source     string
}

// IMemoryRepository is implemented in infrastructure/repository.
type IMemoryRepository interface {
	Save(ctx context.Context, item *MemoryItem) error
	List(ctx context.Context, userID, projectID string, scope Scope, limit int) ([]MemoryItem, error)
	Search(ctx context.Context, userID, projectID, query string, limit int) ([]MemoryItem, error)
	Delete(ctx context.Context, id int64) error
}
