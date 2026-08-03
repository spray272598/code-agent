package audit

import "context"

type Entry struct {
	UserID    string
	SessionID string
	Action    string // tool_call|permission|compress|reflect|error
	Tool      string
	Detail    string
	Decision  string
	LatencyMs int64
}

type Repository interface {
	Append(ctx context.Context, e Entry) error
	ListBySession(ctx context.Context, sessionID string, limit int) ([]Entry, error)
}
