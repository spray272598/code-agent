package host

// WebSocket message protocol between server and host-agent.

type MsgType string

const (
	MsgHello      MsgType = "hello"
	MsgToolCall   MsgType = "tool_call"
	MsgToolResult MsgType = "tool_result"
	MsgPing       MsgType = "ping"
	MsgPong       MsgType = "pong"
	MsgError      MsgType = "error"
)

// Envelope is the JSON wire format.
type Envelope struct {
	Type      MsgType        `json:"type"`
	DeviceID  string         `json:"deviceId,omitempty"`
	CallID    string         `json:"callId,omitempty"`
	Name      string         `json:"name,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	OK        bool           `json:"ok,omitempty"`
	Text      string         `json:"text,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Error     string         `json:"error,omitempty"`
}
