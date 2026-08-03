package host

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PendingCall waits for a host tool_result.
type PendingCall struct {
	Ch chan Envelope
}

// Bridge routes tool calls to a connected host agent over WebSocket.
type Bridge struct {
	mu       sync.RWMutex
	sessions map[string]*HostSession // deviceId -> session
	// default device when only one connected
	pending map[string]*PendingCall // callId -> pending
}

type HostSession struct {
	DeviceID  string
	Workspace string
	Send      func(Envelope) error
	LastSeen  time.Time
}

func NewBridge() *Bridge {
	return &Bridge{
		sessions: map[string]*HostSession{},
		pending:  map[string]*PendingCall{},
	}
}

func (b *Bridge) Register(s *HostSession) {
	b.mu.Lock()
	b.sessions[s.DeviceID] = s
	b.mu.Unlock()
}

func (b *Bridge) Unregister(deviceID string) {
	b.mu.Lock()
	delete(b.sessions, deviceID)
	b.mu.Unlock()
}

func (b *Bridge) Touch(deviceID string) {
	b.mu.Lock()
	if s, ok := b.sessions[deviceID]; ok {
		s.LastSeen = time.Now()
	}
	b.mu.Unlock()
}

func (b *Bridge) ListDevices() []map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]map[string]any, 0, len(b.sessions))
	for _, s := range b.sessions {
		out = append(out, map[string]any{
			"deviceId": s.DeviceID, "workspace": s.Workspace, "lastSeen": s.LastSeen,
		})
	}
	return out
}

func (b *Bridge) OnlineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.sessions)
}

// ResolveResult delivers host tool_result to waiter.
func (b *Bridge) ResolveResult(env Envelope) {
	b.mu.Lock()
	p := b.pending[env.CallID]
	if p != nil {
		delete(b.pending, env.CallID)
	}
	b.mu.Unlock()
	if p != nil {
		select {
		case p.Ch <- env:
		default:
		}
	}
}

// Call sends tool_call to a host and waits for tool_result.
func (b *Bridge) Call(ctx context.Context, deviceID, callID, name string, args map[string]any, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	b.mu.Lock()
	sess := b.pickSession(deviceID)
	if sess == nil {
		b.mu.Unlock()
		return "", fmt.Errorf("no host agent online (deviceId=%q)", deviceID)
	}
	p := &PendingCall{Ch: make(chan Envelope, 1)}
	b.pending[callID] = p
	send := sess.Send
	dev := sess.DeviceID
	b.mu.Unlock()

	env := Envelope{Type: MsgToolCall, DeviceID: dev, CallID: callID, Name: name, Args: args}
	if err := send(env); err != nil {
		b.mu.Lock()
		delete(b.pending, callID)
		b.mu.Unlock()
		return "", err
	}

	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-tctx.Done():
		b.mu.Lock()
		delete(b.pending, callID)
		b.mu.Unlock()
		return "", fmt.Errorf("host tool timeout: %s", name)
	case res := <-p.Ch:
		if !res.OK {
			if res.Error != "" {
				return res.Text, fmt.Errorf("%s", res.Error)
			}
			return res.Text, fmt.Errorf("host tool failed")
		}
		return res.Text, nil
	}
}

func (b *Bridge) pickSession(deviceID string) *HostSession {
	if deviceID != "" {
		return b.sessions[deviceID]
	}
	// first available
	for _, s := range b.sessions {
		return s
	}
	return nil
}
