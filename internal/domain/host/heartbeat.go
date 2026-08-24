package host

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	DefaultHeartbeatInterval = 30 * time.Second
	DefaultSessionTimeout    = 90 * time.Second
	DefaultReconnectAttempts = 3
	DefaultReconnectBaseWait = 5 * time.Second
	DefaultReconnectMaxWait  = 60 * time.Second
)

// HealthStatus represents the health of a host agent connection.
type HealthStatus string

const (
	HealthOnline      HealthStatus = "online"
	HealthDegraded    HealthStatus = "degraded"
	HealthReconnecting HealthStatus = "reconnecting"
	HealthOffline     HealthStatus = "offline"
	HealthUnknown     HealthStatus = "unknown"
)

// HealthInfo contains detailed health information for a host.
type HealthInfo struct {
	DeviceID       string      `json:"deviceId"`
	Status         HealthStatus `json:"status"`
	LastSeen       time.Time   `json:"lastSeen"`
	LastPingRTT    int64       `json:"lastPingRttMs"`
	ConsecutiveFails int       `json:"consecutiveFails"`
	ReconnectCount int         `json:"reconnectCount"`
	Workspace      string      `json:"workspace"`
	Details        string      `json:"details,omitempty"`
}

// HeartbeatConfig configures the heartbeat manager.
type HeartbeatConfig struct {
	Interval         time.Duration
	SessionTimeout   time.Duration
	MaxFails         int
	ReconnectAttempts int
	ReconnectBaseWait time.Duration
	ReconnectMaxWait  time.Duration
}

func DefaultHeartbeatConfig() HeartbeatConfig {
	return HeartbeatConfig{
		Interval:         DefaultHeartbeatInterval,
		SessionTimeout:   DefaultSessionTimeout,
		MaxFails:         3,
		ReconnectAttempts: DefaultReconnectAttempts,
		ReconnectBaseWait: DefaultReconnectBaseWait,
		ReconnectMaxWait:  DefaultReconnectMaxWait,
	}
}

// HeartbeatManager monitors host agent connections and manages reconnections.
type HeartbeatManager struct {
	mu        sync.RWMutex
	bridge    *Bridge
	cfg       HeartbeatConfig
	statuses  map[string]*HealthInfo
	failures  map[string]int
	cancel    context.CancelFunc
	connected bool
}

// NewHeartbeatManager creates a new heartbeat manager.
func NewHeartbeatManager(bridge *Bridge, cfg HeartbeatConfig) *HeartbeatManager {
	return &HeartbeatManager{
		bridge:   bridge,
		cfg:      cfg,
		statuses: make(map[string]*HealthInfo),
		failures: make(map[string]int),
	}
}

// Start begins the heartbeat monitoring loop.
func (hm *HeartbeatManager) Start(ctx context.Context) {
	hm.mu.Lock()
	if hm.connected {
		hm.mu.Unlock()
		return
	}
	ctx, hm.cancel = context.WithCancel(ctx)
	hm.connected = true
	hm.mu.Unlock()

	go hm.loop(ctx)
}

// Stop halts the heartbeat monitoring loop.
func (hm *HeartbeatManager) Stop() {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if hm.cancel != nil {
		hm.cancel()
		hm.cancel = nil
	}
	hm.connected = false
}

// GetHealth returns the health status of all connected hosts.
func (hm *HeartbeatManager) GetHealth() []HealthInfo {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	result := make([]HealthInfo, 0, len(hm.statuses))
	for _, info := range hm.statuses {
		result = append(result, *info)
	}
	return result
}

// GetDeviceHealth returns the health of a specific device.
func (hm *HeartbeatManager) GetDeviceHealth(deviceID string) (HealthInfo, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	info, ok := hm.statuses[deviceID]
	if !ok {
		return HealthInfo{DeviceID: deviceID, Status: HealthUnknown}, false
	}
	return *info, true
}

// IsHealthy checks if a device is in a healthy state.
func (hm *HeartbeatManager) IsHealthy(deviceID string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	info, ok := hm.statuses[deviceID]
	if !ok {
		// Device is registered but not yet processed by heartbeat; treat as healthy
		// by checking if it exists in the bridge
		for _, s := range hm.bridge.ListSessions() {
			if s.DeviceID == deviceID {
				return true
			}
		}
		return false
	}
	return info.Status == HealthOnline || info.Status == HealthDegraded
}

// loop is the main heartbeat monitoring loop.
func (hm *HeartbeatManager) loop(ctx context.Context) {
	ticker := time.NewTicker(hm.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hm.checkAll(ctx)
		}
	}
}

// checkAll checks the health of all connected host sessions.
func (hm *HeartbeatManager) checkAll(ctx context.Context) {
	sessions := hm.bridge.ListSessions()
	now := time.Now()

	for _, sess := range sessions {
		hm.mu.Lock()
		info, exists := hm.statuses[sess.DeviceID]
		if !exists {
			info = &HealthInfo{
				DeviceID:  sess.DeviceID,
				Status:    HealthOnline,
				LastSeen:  now,
				Workspace: sess.Workspace,
			}
			hm.statuses[sess.DeviceID] = info
		}
		hm.mu.Unlock()

		if now.Sub(sess.LastSeen) > hm.cfg.SessionTimeout {
			hm.handleTimeout(ctx, sess)
			continue
		}

		if err := hm.pingDevice(ctx, sess); err != nil {
			hm.handlePingFailure(sess, err)
		} else {
			hm.handlePingSuccess(sess, now)
		}
	}

	hm.cleanupStaleSessions(now)
}

// pingDevice sends a ping to a specific device.
func (hm *HeartbeatManager) pingDevice(ctx context.Context, sess *HostSession) error {
	if sess.Send == nil {
		return fmt.Errorf("no send function for device %s", sess.DeviceID)
	}
	start := time.Now()
	env := Envelope{Type: MsgPing, DeviceID: sess.DeviceID}
	if err := sess.Send(env); err != nil {
		return err
	}

	latency := time.Since(start).Milliseconds()
	hm.mu.Lock()
	if info, exists := hm.statuses[sess.DeviceID]; exists {
		info.LastPingRTT = latency
	}
	hm.mu.Unlock()

	return nil
}

// handlePingSuccess updates the health status after a successful ping.
func (hm *HeartbeatManager) handlePingSuccess(sess *HostSession, now time.Time) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	info, exists := hm.statuses[sess.DeviceID]
	if !exists {
		return
	}

	hm.failures[sess.DeviceID] = 0
	info.ConsecutiveFails = 0
	info.LastSeen = now

	sess.LastSeen = now
	hm.bridge.Touch(sess.DeviceID)

	if info.Status != HealthOnline {
		info.Status = HealthOnline
		info.Details = ""
		log.Printf("[heartbeat] device %s back online\n", sess.DeviceID)
	}
}

// handlePingFailure processes a failed ping.
func (hm *HeartbeatManager) handlePingFailure(sess *HostSession, err error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	info, exists := hm.statuses[sess.DeviceID]
	if !exists {
		return
	}

	hm.failures[sess.DeviceID]++
	info.ConsecutiveFails = hm.failures[sess.DeviceID]

	if info.ConsecutiveFails >= hm.cfg.MaxFails {
		info.Status = HealthReconnecting
		info.Details = fmt.Sprintf("failed %d consecutive pings: %v", info.ConsecutiveFails, err)
		log.Printf("[heartbeat] device %s ping failed (%d/%d): %v\n",
			sess.DeviceID, info.ConsecutiveFails, hm.cfg.MaxFails, err)
		go hm.attemptReconnect(sess)
	} else {
		info.Status = HealthDegraded
		info.Details = fmt.Sprintf("ping failed (%d/%d): %v", info.ConsecutiveFails, hm.cfg.MaxFails, err)
	}
}

// handleTimeout handles a session that has timed out.
func (hm *HeartbeatManager) handleTimeout(ctx context.Context, sess *HostSession) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	info, exists := hm.statuses[sess.DeviceID]
	if !exists {
		return
	}

	info.Status = HealthOffline
	info.Details = "session timed out, attempting reconnect"
	log.Printf("[heartbeat] device %s session timeout, attempting reconnect\n", sess.DeviceID)

	go hm.attemptReconnect(sess)
}

// attemptReconnect tries to reconnect to a failed device with exponential backoff.
func (hm *HeartbeatManager) attemptReconnect(sess *HostSession) {
	for attempt := 0; attempt < hm.cfg.ReconnectAttempts; attempt++ {
		wait := hm.reconnectDelay(attempt)
		log.Printf("[heartbeat] device %s reconnect attempt %d/%d (wait=%v)\n",
			sess.DeviceID, attempt+1, hm.cfg.ReconnectAttempts, wait)

		time.Sleep(wait)

		hm.mu.Lock()
		info, exists := hm.statuses[sess.DeviceID]
		if !exists {
			hm.mu.Unlock()
			return
		}
		info.ReconnectCount++
		hm.mu.Unlock()

		if err := hm.pingDevice(context.Background(), sess); err == nil {
			hm.handlePingSuccess(sess, time.Now())
			return
		}
	}

	hm.mu.Lock()
	if info, exists := hm.statuses[sess.DeviceID]; exists {
		info.Status = HealthOffline
		info.Details = fmt.Sprintf("reconnect failed after %d attempts", hm.cfg.ReconnectAttempts)
	}
	hm.mu.Unlock()

	log.Printf("[heartbeat] device %s permanently offline after %d reconnect attempts\n",
		sess.DeviceID, hm.cfg.ReconnectAttempts)
}

// reconnectDelay calculates exponential backoff delay.
func (hm *HeartbeatManager) reconnectDelay(attempt int) time.Duration {
	delay := hm.cfg.ReconnectBaseWait
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > hm.cfg.ReconnectMaxWait {
			return hm.cfg.ReconnectMaxWait
		}
	}
	return delay
}

// cleanupStaleSessions removes health info for sessions no longer registered.
func (hm *HeartbeatManager) cleanupStaleSessions(now time.Time) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	sessions := hm.bridge.ListSessions()
	activeIDs := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		activeIDs[s.DeviceID] = true
	}

	for id, info := range hm.statuses {
		if !activeIDs[id] {
			if now.Sub(info.LastSeen) > 2*hm.cfg.SessionTimeout {
				delete(hm.statuses, id)
				delete(hm.failures, id)
				log.Printf("[heartbeat] cleaned up stale device %s\n", id)
			}
		}
	}
}

// GracefulDegradationStrategy determines how to handle host failures.
type GracefulDegradationStrategy int

const (
	// DegradeToLocal falls back to local execution when host is unhealthy.
	DegradeToLocal GracefulDegradationStrategy = iota
	// WaitForRecovery retries with timeout before falling back.
	WaitForRecovery
	// FailFast returns error immediately without fallback.
	FailFast
)

// GracefulDegradation wraps a Bridge call with health-aware fallback.
func (hm *HeartbeatManager) GracefulCall(
	ctx context.Context,
	deviceID, callID, name string,
	args map[string]any,
	timeout time.Duration,
	strategy GracefulDegradationStrategy,
	localFallback func(context.Context, map[string]any) (string, error),
) (string, error) {
	if strategy == FailFast {
		return hm.bridge.Call(ctx, deviceID, callID, name, args, timeout)
	}

	if hm.IsHealthy(deviceID) {
		text, err := hm.bridge.Call(ctx, deviceID, callID, name, args, timeout)
		if err == nil {
			return text, nil
		}
		if strategy == WaitForRecovery {
			return hm.retryWithRecovery(ctx, deviceID, callID, name, args, timeout, localFallback)
		}
		if localFallback != nil {
			return localFallback(ctx, args)
		}
		return "", err
	}

	if strategy == WaitForRecovery {
		return hm.retryWithRecovery(ctx, deviceID, callID, name, args, timeout, localFallback)
	}

	if localFallback != nil {
		return localFallback(ctx, args)
	}
	return "", fmt.Errorf("host %s unhealthy and no fallback available", deviceID)
}

func (hm *HeartbeatManager) retryWithRecovery(
	ctx context.Context,
	deviceID, callID, name string,
	args map[string]any,
	timeout time.Duration,
	localFallback func(context.Context, map[string]any) (string, error),
) (string, error) {
	retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-retryCtx.Done():
			if localFallback != nil {
				return localFallback(ctx, args)
			}
			return "", fmt.Errorf("host %s recovery timeout", deviceID)
		case <-ticker.C:
			if hm.IsHealthy(deviceID) {
				return hm.bridge.Call(ctx, deviceID, callID, name, args, timeout)
			}
		}
	}
}