package port

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
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
	// CreatedAt is the creation time for temporal decay scoring.
	CreatedAt time.Time
	// Embedding is the semantic vector of Content (nil when not computed).
	Embedding []float32
}

// IMemoryRepository is implemented in infrastructure/repository.
type IMemoryRepository interface {
	Save(ctx context.Context, item *MemoryItem) error
	List(ctx context.Context, userID, projectID string, scope Scope, limit int) ([]MemoryItem, error)
	Search(ctx context.Context, userID, projectID, query string, limit int) ([]MemoryItem, error)
	Delete(ctx context.Context, id int64) error
	// ListNoEmbedding returns memories that have no stored embedding, for backfill.
	ListNoEmbedding(ctx context.Context, limit int) ([]MemoryItem, error)
	// Prune deletes low-importance memories older than olderThan; returns count removed.
	Prune(ctx context.Context, minImportance int, olderThan time.Time) (int, error)
}

// EncodeEmbedding serializes a vector to a compact string for DB storage.
func EncodeEmbedding(v []float32) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// DecodeEmbedding parses a stored vector string back into []float32.
func DecodeEmbedding(s string) []float32 {
	if s == "" {
		return nil
	}
	var v []float32
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil
	}
	return v
}

// CosineSimilarity returns the cosine similarity between two equal-length vectors.
// Returns 0 when either vector is empty or lengths mismatch.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
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
