package sse

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	IdleHeartbeatInterval   = 30 * time.Second
	ActiveHeartbeatInterval = 5 * time.Second
	MaxMissedHeartbeats     = 3
	DefaultSessionTimeout  = 30 * time.Minute
	HeartbeatCheckInterval  = 1 * time.Second
)

type HeartbeatState int

const (
	HeartbeatIdle HeartbeatState = iota
	HeartbeatActive
	HeartbeatTimeout
	HeartbeatClosed
)

type HeartbeatManager struct {
	mu             sync.Mutex
	state          HeartbeatState
	lastHeartbeat  time.Time
	lastActivity   time.Time
	missedCount    atomic.Int64
	interval       time.Duration
	timeout        time.Duration
	ticker         *time.Ticker
	ctx            context.Context
	cancel         context.CancelFunc
	writer         *SSEStreamWriter
	onTimeout      func()
	activeSince    time.Time
	idleSince      time.Time
	dynamicEnabled bool
	lastSendError  atomic.Int64
}

func NewHeartbeatManager(writer *SSEStreamWriter) *HeartbeatManager {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	return &HeartbeatManager{
		state:          HeartbeatIdle,
		lastHeartbeat:  now,
		lastActivity:   now,
		interval:       IdleHeartbeatInterval,
		timeout:        DefaultSessionTimeout,
		ctx:            ctx,
		cancel:         cancel,
		writer:         writer,
		activeSince:    now,
		idleSince:      now,
		dynamicEnabled: true,
	}
}

func (h *HeartbeatManager) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.ticker = time.NewTicker(HeartbeatCheckInterval)

	go h.runLoop()
}

func (h *HeartbeatManager) runLoop() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.ticker.C:
			h.tick()
		}
	}
}

func (h *HeartbeatManager) tick() {
	h.mu.Lock()

	if h.state == HeartbeatClosed || h.state == HeartbeatTimeout {
		h.mu.Unlock()
		return
	}

	if h.dynamicEnabled {
		h.adjustInterval()
	}

	interval := h.interval
	timeout := h.timeout
	lastHeartbeat := h.lastHeartbeat
	lastActivity := h.lastActivity
	writer := h.writer
	onTimeout := h.onTimeout

	h.mu.Unlock()

	now := time.Now()
	if now.Sub(lastHeartbeat) >= interval {
		if writer != nil {
			sessionID := ""
			if err := writer.WriteHeartbeat(sessionID); err != nil {
				h.lastSendError.Add(1)
			}
			_ = writer.Flush()
			SSEObserveHeartbeat()
		}
		h.mu.Lock()
		h.lastHeartbeat = time.Now()
		h.missedCount.Store(0)
		h.mu.Unlock()
	}

	if timeout > 0 && now.Sub(lastActivity) > timeout {
		h.mu.Lock()
		h.state = HeartbeatTimeout
		h.mu.Unlock()
		if onTimeout != nil {
			onTimeout()
		}
		h.cancel()
	}
}

func (h *HeartbeatManager) adjustInterval() {
	now := time.Now()
	inactiveDuration := now.Sub(h.lastActivity)

	if inactiveDuration > 2*time.Minute {
		h.interval = IdleHeartbeatInterval
		h.state = HeartbeatIdle
	} else if inactiveDuration < 30*time.Second {
		h.interval = ActiveHeartbeatInterval
		h.state = HeartbeatActive
	}
}

func (h *HeartbeatManager) NotifyActivity() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastActivity = time.Now()
	h.lastHeartbeat = time.Now()
	h.missedCount.Store(0)
}

func (h *HeartbeatManager) SetOnTimeout(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onTimeout = fn
}

func (h *HeartbeatManager) SetDynamicEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dynamicEnabled = enabled
}

func (h *HeartbeatManager) State() HeartbeatState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *HeartbeatManager) CurrentInterval() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interval
}

func (h *HeartbeatManager) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.state = HeartbeatClosed
	if h.ticker != nil {
		h.ticker.Stop()
	}
	h.cancel()
}

func (h *HeartbeatManager) LastHeartbeatTime() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastHeartbeat
}

func (h *HeartbeatManager) LastActivityTime() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastActivity
}

func (h *HeartbeatManager) SessionDuration() time.Duration {
	return time.Since(h.activeSince)
}
