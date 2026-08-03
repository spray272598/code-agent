package host

// Mode of tool execution.
type Mode string

const (
	ModeServer Mode = "server" // tools run on server workspace (default)
	ModeHost   Mode = "host"   // prefer host-agent WebSocket when online
)

// Executor abstracts where tools conceptually run.
type Executor interface {
	Mode() Mode
	WorkspaceRoot() string
}

// ServerExecutor runs tools against a server-side workspace root.
type ServerExecutor struct {
	Root string
}

func (e *ServerExecutor) Mode() Mode            { return ModeServer }
func (e *ServerExecutor) WorkspaceRoot() string { return e.Root }

// HostExecutor marks prefer-host mode; actual routing is via Bridge + ProxyTool.
type HostExecutor struct {
	Endpoint     string
	FallbackRoot string
}

func (e *HostExecutor) Mode() Mode { return ModeHost }
func (e *HostExecutor) WorkspaceRoot() string {
	if e.FallbackRoot != "" {
		return e.FallbackRoot
	}
	return "./workspace"
}
