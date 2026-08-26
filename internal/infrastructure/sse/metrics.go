package sse

import (
	"sync"
	"sync/atomic"
	"time"
)

type ConnectionMetrics struct {
	ID            string
	SessionID     string
	StartTime     time.Time
	LastEventTime time.Time
	EventCount    atomic.Int64
	BytesSent     atomic.Int64
	EventsDropped atomic.Int64
	Reconnects    atomic.Int64
	EventsByType  sync.Map
	LastEventType string
}

func NewConnectionMetrics(id, sessionID string) *ConnectionMetrics {
	now := time.Now()
	return &ConnectionMetrics{
		ID:            id,
		SessionID:     sessionID,
		StartTime:     now,
		LastEventTime: now,
	}
}

func (m *ConnectionMetrics) RecordEvent(eventType EventType, dataSize int) {
	m.EventCount.Add(1)
	m.BytesSent.Add(int64(dataSize))
	m.LastEventTime = time.Now()
	m.LastEventType = string(eventType)

	if v, ok := m.EventsByType.Load(string(eventType)); ok {
		if count, ok := v.(*atomic.Int64); ok {
			count.Add(1)
		}
	} else {
		count := &atomic.Int64{}
		count.Store(1)
		m.EventsByType.Store(string(eventType), count)
	}
}

func (m *ConnectionMetrics) RecordDrop() {
	m.EventsDropped.Add(1)
}

func (m *ConnectionMetrics) RecordReconnect() {
	m.Reconnects.Add(1)
}

func (m *ConnectionMetrics) Duration() time.Duration {
	return time.Since(m.StartTime)
}

func (m *ConnectionMetrics) EventsByTypeSnapshot() map[string]int64 {
	result := make(map[string]int64)
	m.EventsByType.Range(func(key, value interface{}) bool {
		if count, ok := value.(*atomic.Int64); ok {
			result[key.(string)] = count.Load()
		}
		return true
	})
	return result
}

type ConnectionPool struct {
	mu             sync.RWMutex
	connections    map[string]*ConnectionMetrics
	maxConnections int
	activeCount    atomic.Int64
	totalCreated   atomic.Int64
	totalClosed    atomic.Int64
	totalEvents    atomic.Int64
	totalBytes     atomic.Int64
	totalDropped   atomic.Int64
}

func NewConnectionPool(maxConnections int) *ConnectionPool {
	if maxConnections <= 0 {
		maxConnections = 100
	}
	return &ConnectionPool{
		connections:    make(map[string]*ConnectionMetrics),
		maxConnections: maxConnections,
	}
}

func (p *ConnectionPool) Register(id, sessionID string) (*ConnectionMetrics, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.connections) >= p.maxConnections {
		return nil, ErrPoolFull
	}

	metrics := NewConnectionMetrics(id, sessionID)
	p.connections[id] = metrics
	p.activeCount.Add(1)
	p.totalCreated.Add(1)
	return metrics, nil
}

func (p *ConnectionPool) Unregister(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.connections[id]; ok {
		delete(p.connections, id)
		p.activeCount.Add(-1)
		p.totalClosed.Add(1)
	}
}

func (p *ConnectionPool) Get(id string) *ConnectionMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connections[id]
}

func (p *ConnectionPool) ActiveCount() int64 {
	return p.activeCount.Load()
}

func (p *ConnectionPool) TotalCreated() int64 {
	return p.totalCreated.Load()
}

func (p *ConnectionPool) TotalClosed() int64 {
	return p.totalClosed.Load()
}

func (p *ConnectionPool) RecordEvent(eventType EventType, dataSize int) {
	p.totalEvents.Add(1)
	p.totalBytes.Add(int64(dataSize))
}

func (p *ConnectionPool) RecordDrop() {
	p.totalDropped.Add(1)
}

type PoolStats struct {
	Active       int64
	Max          int
	TotalCreated int64
	TotalClosed  int64
	TotalEvents  int64
	TotalBytes   int64
	TotalDropped int64
	UsageRate    float64
}

func (p *ConnectionPool) Stats() PoolStats {
	active := p.activeCount.Load()
	totalCreated := p.totalCreated.Load()

	var usageRate float64
	if p.maxConnections > 0 {
		usageRate = float64(active) / float64(p.maxConnections)
	}

	return PoolStats{
		Active:       active,
		Max:          p.maxConnections,
		TotalCreated: totalCreated,
		TotalClosed:  p.totalClosed.Load(),
		TotalEvents:  p.totalEvents.Load(),
		TotalBytes:   p.totalBytes.Load(),
		TotalDropped: p.totalDropped.Load(),
		UsageRate:    usageRate,
	}
}

func (p *ConnectionPool) ListActive() []*ConnectionMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*ConnectionMetrics, 0, len(p.connections))
	for _, m := range p.connections {
		result = append(result, m)
	}
	return result
}
