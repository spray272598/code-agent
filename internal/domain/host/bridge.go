package host

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type PendingCall struct {
	Ch chan Envelope
}

type Bridge struct {
	mu       sync.RWMutex
	sessions map[string]*HostSession
	pending  map[string]*PendingCall
	hbMgr    *HeartbeatManager
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

func NewBridgeWithHeartbeat(cfg HeartbeatConfig) *Bridge {
	b := &Bridge{
		sessions: map[string]*HostSession{},
		pending:  map[string]*PendingCall{},
	}
	b.hbMgr = NewHeartbeatManager(b, cfg)
	return b
}

func (b *Bridge) StartHeartbeat(ctx context.Context) {
	if b.hbMgr != nil {
		b.hbMgr.Start(ctx)
	}
}

func (b *Bridge) StopHeartbeat() {
	if b.hbMgr != nil {
		b.hbMgr.Stop()
	}
}

func (b *Bridge) HeartbeatManager() *HeartbeatManager {
	return b.hbMgr
}

// SetMCPHealthReporter wires an MCP health reporter into the heartbeat
// manager so combined host+MCP health snapshots are available via
// HeartbeatManager.GetCombinedHealth().
func (b *Bridge) SetMCPHealthReporter(r MCPHealthReporter) {
	if b.hbMgr != nil {
		b.hbMgr.SetMCPHealthReporter(r)
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

// ListSessions returns a copy of all host sessions.
func (b *Bridge) ListSessions() []*HostSession {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]*HostSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		cp := *s
		out = append(out, &cp)
	}
	return out
}

func (b *Bridge) OnlineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.sessions)
}

// OnlineCountHealthy returns the count of sessions that are currently healthy.
func (b *Bridge) OnlineCountHealthy() int {
	if b.hbMgr == nil {
		return b.OnlineCount()
	}
	count := 0
	for _, s := range b.sessions {
		if b.hbMgr.IsHealthy(s.DeviceID) {
			count++
		}
	}
	return count
}

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

// CallWithDegradation calls a tool with graceful degradation on health failure.
func (b *Bridge) CallWithDegradation(
	ctx context.Context,
	deviceID, callID, name string,
	args map[string]any,
	timeout time.Duration,
	strategy GracefulDegradationStrategy,
	localFallback func(context.Context, map[string]any) (string, error),
) (string, error) {
	if b.hbMgr != nil {
		return b.hbMgr.GracefulCall(ctx, deviceID, callID, name, args, timeout, strategy, localFallback)
	}
	return b.Call(ctx, deviceID, callID, name, args, timeout)
}

func (b *Bridge) pickSession(deviceID string) *HostSession {
	if deviceID != "" {
		return b.sessions[deviceID]
	}
	for _, s := range b.sessions {
		return s
	}
	return nil
}
