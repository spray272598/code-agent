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
	// ListBySession returns entries belonging to userID; sessionID filters further
	// (empty sessionID means "all sessions for this user"). Callers MUST pass the
	// authenticated principal's userID — passing another user's id returns nothing
	// (Sprint 1.7 multi-tenant isolation guarantee).
	ListBySession(ctx context.Context, userID, sessionID string, limit int) ([]Entry, error)
	// ListForUser is the ctx-driven (Sprint 1.6) form: the userID is extracted
	// from tenant.From(ctx). Use this from HTTP handlers / business code that
	// already runs after authJWT. ListBySession stays for callers that build
	// queries outside the request scope (cron, admin tools, tests).
	ListForUser(ctx context.Context, sessionID string, limit int) ([]Entry, error)
}
