package host

import "context"

// Mode of tool execution.
type Mode string

const (
	ModeServer Mode = "server" // tools run on server workspace (default)
	ModeHost   Mode = "host"   // future: side-car on developer machine
)

// Executor abstracts where tools run.
// Phase 6: ServerExecutor is production path; HostExecutor is a stub for roadmap.
type Executor interface {
	Mode() Mode
	// WorkspaceRoot returns the effective root for file/shell tools.
	WorkspaceRoot() string
}

// ServerExecutor runs tools against a server-side workspace root.
type ServerExecutor struct {
	Root string
}

func (e *ServerExecutor) Mode() Mode             { return ModeServer }
func (e *ServerExecutor) WorkspaceRoot() string  { return e.Root }

// HostExecutor is a placeholder for a future WebSocket/gRPC side-car.
// Registering it only changes Mode() reporting until a real host client is wired.
type HostExecutor struct {
	// Endpoint of the host agent (e.g. ws://127.0.0.1:9090)
	Endpoint string
	// Fallback root when host offline
	FallbackRoot string
}

func (e *HostExecutor) Mode() Mode { return ModeHost }
func (e *HostExecutor) WorkspaceRoot() string {
	if e.FallbackRoot != "" {
		return e.FallbackRoot
	}
	return "./workspace"
}

// Dial is not implemented in Phase 6 (explicit stub).
func (e *HostExecutor) Dial(ctx context.Context) error {
	_ = ctx
	return ErrHostNotImplemented
}

// ErrHostNotImplemented signals Host Executor is roadmap-only for now.
var ErrHostNotImplemented = errString("host executor not implemented: use server workspace or wait for host agent side-car")

type errString string

func (e errString) Error() string { return string(e) }
