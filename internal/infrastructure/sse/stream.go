package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	MaxStreamBytes     = 8 * 1024 * 1024
	DefaultFlushInterval = 100 * time.Millisecond
	MinFlushBytes        = 4096
)

type UTF8SafeSlicer struct{}

func (s *UTF8SafeSlicer) Slice(data string, maxBytes int) (string, int) {
	if maxBytes <= 0 {
		return "", 0
	}
	dataLen := len(data)
	if dataLen <= maxBytes {
		return data, dataLen
	}

	cut := maxBytes
	for cut > 0 && !utf8.ValidString(data[:cut]) {
		cut--
	}

	if cut > 0 {
		for cut < dataLen && !utf8.RuneStart(data[cut]) {
			cut++
		}
	}

	if cut > dataLen {
		cut = dataLen
	}

	for cut > 0 && !utf8.RuneStart(data[cut-1]) {
		cut--
	}

	if cut == 0 && dataLen > 0 {
		r, size := utf8.DecodeRuneInString(data)
		if r != utf8.RuneError {
			return string(r), size
		}
		return string(data[0]), 1
	}

	return data[:cut], cut
}

type ByteBudget struct {
	maxBytes  int64
	usedBytes atomic.Int64
	truncated atomic.Bool
}

func NewByteBudget(maxBytes int64) *ByteBudget {
	if maxBytes <= 0 {
		maxBytes = MaxStreamBytes
	}
	return &ByteBudget{
		maxBytes: maxBytes,
	}
}

func (b *ByteBudget) AddBytes(data []byte) (int, bool) {
	dataLen := int64(len(data))
	current := b.usedBytes.Load()
	if current+dataLen > b.maxBytes {
		b.truncated.Store(true)
		remaining := b.maxBytes - current
		if remaining <= 0 {
			return 0, true
		}
		return int(remaining), true
	}
	b.usedBytes.Add(dataLen)
	return len(data), false
}

func (b *ByteBudget) AddString(s string) (int, bool) {
	return b.AddBytes([]byte(s))
}

func (b *ByteBudget) Used() int64 {
	return b.usedBytes.Load()
}

func (b *ByteBudget) Remaining() int64 {
	remaining := b.maxBytes - b.usedBytes.Load()
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (b *ByteBudget) Truncated() bool {
	return b.truncated.Load()
}

func (b *ByteBudget) Reset() {
	b.usedBytes.Store(0)
	b.truncated.Store(false)
}

type SSEStreamWriter struct {
	w           http.ResponseWriter
	flusher     http.Flusher
	budget      *ByteBudget
	slicer      *UTF8SafeSlicer
	seq         atomic.Uint64
	lastFlush   time.Time
	pendingBuf  []byte
	pendingCount int
	mu          sync.Mutex
}

func NewSSEStreamWriter(w http.ResponseWriter, flusher http.Flusher) *SSEStreamWriter {
	return &SSEStreamWriter{
		w:      w,
		flusher: flusher,
		budget:  NewByteBudget(MaxStreamBytes),
		slicer:  &UTF8SafeSlicer{},
	}
}

func (s *SSEStreamWriter) ResponseWriter() http.ResponseWriter {
	return s.w
}

func (s *SSEStreamWriter) Flusher() http.Flusher {
	return s.flusher
}

func (s *SSEStreamWriter) WriteEvent(ev *StructuredEvent) error {
	if ev == nil {
		return fmt.Errorf("nil event")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if ev.Seq == 0 {
		ev.Seq = s.seq.Add(1)
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	eventName := ev.SSEEventName()
	eventID := ev.SSEEventID()

	var frame string
	if eventID != "" {
		frame = fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", eventID, eventName, string(data))
	} else {
		frame = fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, string(data))
	}

	written, truncated := s.budget.AddString(frame)
	if truncated && written < len(frame) {
		if err := s.flushLocked(); err != nil {
			return err
		}
		s.pendingBuf = nil
		s.pendingCount = 0
		s.budget.AddString(frame[:written])
	}

	s.pendingBuf = append(s.pendingBuf, []byte(frame)...)
	s.pendingCount++

	return nil
}

func (s *SSEStreamWriter) WriteComment(comment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	frame := fmt.Sprintf(": %s\n\n", comment)
	s.pendingBuf = append(s.pendingBuf, []byte(frame)...)
	s.pendingCount++
	return nil
}

func (s *SSEStreamWriter) WriteHeartbeat(sessionID string) error {
	ev := NewHeartbeatEvent(sessionID, s.seq.Add(1))
	return s.WriteEvent(ev)
}

func (s *SSEStreamWriter) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *SSEStreamWriter) flushLocked() error {
	if len(s.pendingBuf) == 0 {
		return nil
	}

	_, err := s.w.Write(s.pendingBuf)
	if err != nil {
		return fmt.Errorf("write flush: %w", err)
	}

	s.flusher.Flush()
	s.lastFlush = time.Now()
	s.pendingBuf = nil
	s.pendingCount = 0
	return nil
}

func (s *SSEStreamWriter) Close() {
	ev := NewDoneEvent("", s.seq.Add(1))
	if err := s.WriteEvent(ev); err == nil {
		_ = s.Flush()
	}
}

func (s *SSEStreamWriter) Seq() uint64 {
	return s.seq.Load()
}

func (s *SSEStreamWriter) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingCount
}

func (s *SSEStreamWriter) LastFlushTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFlush
}

type AdaptiveFlusher struct {
	writer     *SSEStreamWriter
	interval   time.Duration
	ticker     *time.Ticker
	done       chan struct{}
	lastActive time.Time
}

func NewAdaptiveFlusher(writer *SSEStreamWriter, interval time.Duration) *AdaptiveFlusher {
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	return &AdaptiveFlusher{
		writer:   writer,
		interval: interval,
		ticker:   time.NewTicker(interval),
		done:     make(chan struct{}),
	}
}

func (f *AdaptiveFlusher) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				_ = f.writer.Flush()
				return
			case <-f.ticker.C:
				if f.writer.PendingCount() > 0 {
					_ = f.writer.Flush()
				}
			case <-f.done:
				_ = f.writer.Flush()
				return
			}
		}
	}()
}

func (f *AdaptiveFlusher) Stop() {
	f.ticker.Stop()
	close(f.done)
}

func (f *AdaptiveFlusher) FlushNow() {
	_ = f.writer.Flush()
}

func SSEContentType() string {
	return "text/event-stream"
}

func SetSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func WriteSSEEvent(w http.ResponseWriter, flusher http.Flusher, ev *StructuredEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	eventName := ev.SSEEventName()
	eventID := ev.SSEEventID()

	var frame string
	if eventID != "" {
		frame = fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", eventID, eventName, string(data))
	} else {
		frame = fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, string(data))
	}

	_, err = fmt.Fprint(w, frame)
	if err != nil {
		return err
	}

	flusher.Flush()
	return nil
}

func WriteSSEComment(w http.ResponseWriter, flusher http.Flusher, comment string) error {
	frame := fmt.Sprintf(": %s\n\n", comment)
	_, err := fmt.Fprint(w, frame)
	if err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (s *SSEStreamWriter) Write(p []byte) (n int, err error) {
	n, err = s.w.Write(p)
	if err != nil {
		return
	}
	s.flusher.Flush()
	return
}
