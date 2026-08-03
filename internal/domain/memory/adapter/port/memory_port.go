package port

import (
	"context"
	"strings"
)

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

// Tokenize splits query into lowercase tokens for simple search scoring.
func Tokenize(q string) []string {
	q = strings.ToLower(q)
	var cur strings.Builder
	var out []string
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			if r >= 'A' && r <= 'Z' {
				r += 32
			}
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}
