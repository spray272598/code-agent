package sse

import (
	"sync/atomic"
)

const (
	DefaultBufferSize = 256
	HighWaterMark     = 0.8
	LowWaterMark      = 0.3
)

type EventPriority int

const (
	PriorityCritical EventPriority = iota
	PriorityNormal
	PriorityLow
)

var criticalEvents = map[EventType]bool{
	EventDone:       true,
	EventError:      true,
	EventCancel:     true,
	EventSystem:     true,
	EventPermission: true,
	EventCheckpoint: true,
}

var lowPriorityEvents = map[EventType]bool{
	EventReasoningDelta: true,
	EventTextDelta:      true,
}

func GetEventPriority(t EventType) EventPriority {
	if criticalEvents[t] {
		return PriorityCritical
	}
	if lowPriorityEvents[t] {
		return PriorityLow
	}
	return PriorityNormal
}

type BackpressureBuffer struct {
	ch         chan *StructuredEvent
	size       int
	highMark   int
	lowMark    int
	dropCount  atomic.Int64
	droppedOld atomic.Int64
}

func NewBackpressureBuffer(size int) *BackpressureBuffer {
	if size <= 0 {
		size = DefaultBufferSize
	}
	return &BackpressureBuffer{
		ch:       make(chan *StructuredEvent, size),
		size:     size,
		highMark: int(float64(size) * HighWaterMark),
		lowMark:  int(float64(size) * LowWaterMark),
	}
}

func (b *BackpressureBuffer) Send(ev *StructuredEvent) bool {
	if ev == nil {
		return false
	}

	priority := GetEventPriority(ev.Type)
	current := len(b.ch)

	if current >= b.highMark && priority == PriorityLow {
		b.dropCount.Add(1)
		b.droppedOld.Add(1)
		ev.Truncated = true
		return false
	}

	if current >= b.size {
		if priority == PriorityCritical {
			select {
			case <-b.ch:
				b.droppedOld.Add(1)
			default:
			}
		} else {
			b.dropCount.Add(1)
			return false
		}
	}

	select {
	case b.ch <- ev:
		return true
	default:
		if priority == PriorityCritical {
			<-b.ch
			b.ch <- ev
			b.droppedOld.Add(1)
			return true
		}
		b.dropCount.Add(1)
		return false
	}
}

func (b *BackpressureBuffer) Receive() (*StructuredEvent, bool) {
	ev, ok := <-b.ch
	return ev, ok
}

func (b *BackpressureBuffer) TryReceive() (*StructuredEvent, bool) {
	select {
	case ev, ok := <-b.ch:
		return ev, ok
	default:
		return nil, false
	}
}

func (b *BackpressureBuffer) Close() {
	close(b.ch)
}

func (b *BackpressureBuffer) Len() int {
	return len(b.ch)
}

func (b *BackpressureBuffer) Cap() int {
	return cap(b.ch)
}

func (b *BackpressureBuffer) Usage() float64 {
	if b.size == 0 {
		return 0
	}
	return float64(len(b.ch)) / float64(b.size)
}

func (b *BackpressureBuffer) IsHighWatermark() bool {
	return len(b.ch) >= b.highMark
}

func (b *BackpressureBuffer) IsLowWatermark() bool {
	return len(b.ch) <= b.lowMark
}

func (b *BackpressureBuffer) DropCount() int64 {
	return b.dropCount.Load()
}

func (b *BackpressureBuffer) DroppedOldCount() int64 {
	return b.droppedOld.Load()
}

type BufferStats struct {
	Len        int
	Cap        int
	Usage      float64
	HighWater  bool
	LowWater   bool
	DropCount  int64
	DroppedOld int64
}

func (b *BackpressureBuffer) Stats() BufferStats {
	return BufferStats{
		Len:        len(b.ch),
		Cap:        cap(b.ch),
		Usage:      b.Usage(),
		HighWater:  b.IsHighWatermark(),
		LowWater:   b.IsLowWatermark(),
		DropCount:  b.dropCount.Load(),
		DroppedOld: b.droppedOld.Load(),
	}
}
