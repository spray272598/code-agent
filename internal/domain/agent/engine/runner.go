package engine

import (
	"context"

	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
)

// Runner is the agent orchestration entry (native Loop or Eino-backed).
// Application depends on this port — not on a concrete framework.
type Runner interface {
	Run(ctx context.Context, session *sessmodel.Session, userInput string, eventCh chan<- *Event, opts RunOptions) (*Result, error)
	Permission() *security.Guard
}

// Ensure *Loop implements Runner.
var _ Runner = (*Loop)(nil)
