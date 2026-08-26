package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/observability"
)

type SSEHandler struct {
	pool       *ConnectionPool
	seqCounter atomic.Uint64
	defaultHWM float64
	defaultLWM float64
	defaultBuf int
}

func NewSSEHandler() *SSEHandler {
	return &SSEHandler{
		pool:       NewConnectionPool(50),
		defaultHWM: HighWaterMark,
		defaultLWM: LowWaterMark,
		defaultBuf: DefaultBufferSize,
	}
}

func (h *SSEHandler) Pool() *ConnectionPool {
	return h.pool
}

func (h *SSEHandler) SetPoolSize(max int) {
	h.pool = NewConnectionPool(max)
}

type SSEConnection struct {
	id         string
	sessionID  string
	writer     *SSEStreamWriter
	buffer     *BackpressureBuffer
	heartbeat  *HeartbeatManager
	metrics    *ConnectionMetrics
	handler    *SSEHandler
	seq        uint64
	lastEvent  time.Time
	eventCount atomic.Int64
	bytesSent  atomic.Int64
	detector   *DoomLoopDetector
	capture    *StreamingTurnCapture
	active     bool
}

func (h *SSEHandler) NewConnection(w http.ResponseWriter, r *http.Request, sessionID string) (*SSEConnection, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported: ResponseWriter does not implement http.Flusher")
	}

	SetSSEHeaders(w)

	connID := fmt.Sprintf("conn-%d", time.Now().UnixNano())

	conn := &SSEConnection{
		id:        connID,
		sessionID: sessionID,
		writer:    NewSSEStreamWriter(w, flusher),
		buffer:    NewBackpressureBuffer(h.defaultBuf),
		handler:   h,
		lastEvent: time.Now(),
		detector:  NewDoomLoopDetector(),
		capture:   NewStreamingTurnCapture(DefaultDoomMaxStreamBytes),
	}

	metrics, err := h.pool.Register(connID, sessionID)
	if err != nil {
		return nil, err
	}
	conn.metrics = metrics

	SSEObserveConnection()
	SSEObserveReconnectSuccess()

	return conn, nil
}

func (c *SSEConnection) Start(ctx context.Context) error {
	SetSSEHeaders(c.writer.ResponseWriter())
	c.active = true

	sessionEvent := NewStructuredEvent(EventSystem, c.sessionID, c.NextSeq())
	sessionEvent.Content = "stream-started"
	sessionEvent.Data = mustJSON(map[string]any{
		"sessionId": c.sessionID,
		"connId":    c.id,
		"time":      time.Now().UnixMilli(),
	})

	if err := c.WriteEvent(sessionEvent); err != nil {
		return err
	}

	c.heartbeat = NewHeartbeatManager(c.writer)
	c.heartbeat.NotifyActivity()
	c.heartbeat.Start()
	SSEObserveHeartbeat()

	go func() {
		<-ctx.Done()
		c.Close()
	}()

	return c.Flush()
}

func (c *SSEConnection) NextSeq() uint64 {
	c.seq++
	return c.seq
}

func (c *SSEConnection) WriteEvent(ev *StructuredEvent) error {
	if ev.Seq == 0 {
		ev.Seq = c.NextSeq()
	}

	ev.SessionID = c.sessionID

	start := time.Now()

	if c.detector != nil && (ev.Type == EventReasoningDelta || ev.Type == EventTextDelta) {
		isReasoning := ev.Type == EventReasoningDelta
		content := ev.Content
		if content == "" && len(ev.Delta) > 0 {
			content = ev.Delta
		}
		c.detector.RecordDelta(content, isReasoning)

		if c.detector.IsDoomed() {
			SSEObserveDoomLoop()
			abortEv := NewErrorEvent(c.sessionID, c.NextSeq(), "doomloop detected: "+c.detector.AbortReason())
			abortEv.Completed = true
			ev = abortEv
		}
	}

	err := c.writer.WriteEvent(ev)
	if err != nil {
		return err
	}

	c.lastEvent = start
	c.eventCount.Add(1)
	SSEObserveEvent()

	dataBytes, _ := json.Marshal(ev)
	c.bytesSent.Add(int64(len(dataBytes)))
	SSEObserveBytes(int64(len(dataBytes)))

	if c.metrics != nil {
		c.metrics.RecordEvent(ev.Type, len(dataBytes))
	}

	c.handler.pool.RecordEvent(ev.Type, len(dataBytes))

	if c.heartbeat != nil {
		c.heartbeat.NotifyActivity()
	}

	return nil
}

func (c *SSEConnection) WriteEngineEvent(ev *engine.Event) error {
	if ev == nil {
		return nil
	}

	sseEvent := c.ConvertEngineEvent(ev)
	if sseEvent != nil {
		return c.WriteEvent(sseEvent)
	}
	return nil
}

func (c *SSEConnection) ConvertEngineEvent(ev *engine.Event) *StructuredEvent {
	var eventType EventType

	switch ev.Type {
	case engine.EventThought:
		eventType = EventReasoningDelta
	case engine.EventTextDelta:
		eventType = EventTextDelta
	case engine.EventToolCall:
		eventType = EventToolCallDelta
	case engine.EventToolResult:
		eventType = EventToolResult
	case engine.EventAnswer:
		eventType = EventAnswer
	case engine.EventDone:
		eventType = EventDone
	case engine.EventError:
		eventType = EventError
	case engine.EventPermission:
		eventType = EventPermission
	case engine.EventCheckpoint:
		eventType = EventCheckpoint
	case engine.EventCancel:
		eventType = EventCancel
	case engine.EventPlan, engine.EventPlanUpdate, engine.EventReplan:
		eventType = EventPlan
	case engine.EventCompress:
		eventType = EventSystem
	case engine.EventSubAgent:
		eventType = EventSystem
	default:
		eventType = EventSystem
	}

	sseEv := NewStructuredEvent(eventType, c.sessionID, c.NextSeq())
	sseEv.Step = ev.Step
	sseEv.Content = ev.Content
	sseEv.Completed = ev.Completed
	sseEv.Timestamp = ev.Timestamp

	if ev.Data != nil {
		if jsonData, err := json.Marshal(ev.Data); err == nil {
			sseEv.Data = jsonData
		}
	}

	if eventType == EventReasoningDelta && ev.Content != "" {
		sseEv.Reasoning = &ReasoningMeta{
			Delta: ev.Content,
		}
	}

	if eventType == EventToolCallDelta {
		sseEv.Data = mustJSON(map[string]any{
			"tool": ev.Content,
			"step": ev.Step,
			"args": ev.Data,
		})
	}

	if eventType == EventError {
		sseEv.Completed = true
	}

	if eventType == EventDone {
		sseEv.Completed = true
	}

	return sseEv
}

func (c *SSEConnection) Flush() error {
	return c.writer.Flush()
}

func (c *SSEConnection) Close() {
	if c.heartbeat != nil {
		c.heartbeat.Stop()
	}

	if c.capture != nil {
		c.capture.FinalizeForUpload()
		if c.detector != nil && c.detector.IsDoomed() {
			c.capture.DoomLoop = &DoomLoopSegmentStamp{
				Action:  "closed_due_to_doomloop",
				Attempt: c.detector.attemptCount,
			}
		}
	}

	ev := NewDoneEvent(c.sessionID, c.NextSeq())
	_ = c.writer.WriteEvent(ev)
	_ = c.writer.Flush()

	c.handler.pool.Unregister(c.id)
	SSEObserveDisconnection()
	c.active = false
}

func (c *SSEConnection) LastEventTime() time.Time {
	return c.lastEvent
}

func (c *SSEConnection) EventCount() int64 {
	return c.eventCount.Load()
}

func (c *SSEConnection) BytesSent() int64 {
	return c.bytesSent.Load()
}

func (c *SSEConnection) ID() string {
	return c.id
}

func (c *SSEConnection) SessionID() string {
	return c.sessionID
}

func (c *SSEConnection) Metrics() *ConnectionMetrics {
	return c.metrics
}

func (c *SSEConnection) Buffer() *BackpressureBuffer {
	return c.buffer
}

func (c *SSEConnection) Budget() *ByteBudget {
	return c.writer.budget
}

func (c *SSEConnection) WriteComment(comment string) error {
	return c.writer.WriteComment(comment)
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return data
}

type StreamOptions struct {
	SessionID     string
	AutoReconnect bool
	LastEventID   string
}

func DefaultStreamOptions() StreamOptions {
	return StreamOptions{
		AutoReconnect: true,
	}
}

type SSEStreamRunner struct {
	handler *SSEHandler
	conn    *SSEConnection
	ctx     context.Context
	cancel  context.CancelFunc
	errCh   chan error
	eventCh chan *engine.Event
	doneCh  chan struct{}
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (h *SSEHandler) NewStreamRunner(w http.ResponseWriter, r *http.Request, sessionID string) (*SSEStreamRunner, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}

	SetSSEHeaders(w)

	ctx, cancel := context.WithCancel(r.Context())

	connID := fmt.Sprintf("conn-%d", time.Now().UnixNano())

	conn := &SSEConnection{
		id:        connID,
		sessionID: sessionID,
		writer:    NewSSEStreamWriter(w, flusher),
		buffer:    NewBackpressureBuffer(h.defaultBuf),
		handler:   h,
		lastEvent: time.Now(),
		detector:  NewDoomLoopDetector(),
		capture:   NewStreamingTurnCapture(DefaultDoomMaxStreamBytes),
	}

	metrics, err := h.pool.Register(connID, sessionID)
	if err != nil {
		cancel()
		return nil, err
	}
	conn.metrics = metrics

	SSEObserveConnection()

	runner := &SSEStreamRunner{
		handler: h,
		conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		errCh:   make(chan error, 1),
		eventCh: make(chan *engine.Event, 256),
		doneCh:  make(chan struct{}),
		writer:  w,
		flusher: flusher,
	}

	return runner, nil
}

func (r *SSEStreamRunner) Start() {
	go r.run()
}

func (r *SSEStreamRunner) SendEvent(ev *engine.Event) {
	select {
	case r.eventCh <- ev:
	default:
		if r.conn.metrics != nil {
			r.conn.metrics.RecordDrop()
		}
		r.handler.pool.RecordDrop()
		observability.Warnf("sse event buffer full, dropping event type=%s", ev.Type)
	}
}

func (r *SSEStreamRunner) Close() {
	r.cancel()
	r.conn.Close()
}

func (r *SSEStreamRunner) GracefulClose(timeout time.Duration) error {
	if timeout <= 0 {
		r.Close()
		return nil
	}

	r.cancel()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	select {
	case <-r.doneCh:
		r.conn.Close()
		return nil
	case <-deadline.C:
		r.conn.Close()
		return context.DeadlineExceeded
	}
}

func (r *SSEStreamRunner) Wait() error {
	select {
	case <-r.doneCh:
		return nil
	case err := <-r.errCh:
		return err
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

func (r *SSEStreamRunner) DoneCh() <-chan struct{} {
	return r.doneCh
}

func (r *SSEStreamRunner) Buffer() *BackpressureBuffer {
	return r.conn.buffer
}

func (r *SSEStreamRunner) Metrics() *ConnectionMetrics {
	return r.conn.metrics
}

func (r *SSEStreamRunner) run() {
	defer close(r.doneCh)
	defer SSEObserveDisconnection()

	sessionEvent := NewStructuredEvent(EventSystem, r.conn.sessionID, r.conn.NextSeq())
	sessionEvent.Content = "stream-started"
	sessionEvent.Data = mustJSON(map[string]any{
		"sessionId": r.conn.sessionID,
		"connId":    r.conn.id,
		"time":      time.Now().UnixMilli(),
	})

	if err := r.conn.WriteEvent(sessionEvent); err != nil {
		r.errCh <- err
		return
	}
	_ = r.conn.Flush()

	heartbeat := NewHeartbeatManager(r.conn.writer)
	heartbeat.Start()
	SSEObserveHeartbeat()
	defer heartbeat.Stop()

	adaptive := NewAdaptiveFlusher(r.conn.writer, DefaultFlushInterval)
	adaptive.Start(r.ctx)
	defer adaptive.Stop()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	batchBuf := make([]*engine.Event, 0, 16)
	lastFlush := time.Now()
	lastReasoningType := ""
	captureStarted := false
	var turnStartMs int64

	for {
		select {
		case <-r.ctx.Done():
			if len(batchBuf) > 0 {
				r.flushBatch(&batchBuf)
			}
			if r.conn.capture != nil {
				r.conn.capture.FinalizeForUpload()
			}
			r.sendDone()
			return

		case <-ticker.C:
			if time.Since(lastFlush) >= 100*time.Millisecond && len(batchBuf) > 0 {
				r.flushBatch(&batchBuf)
				lastFlush = time.Now()
			}
			if r.conn.buffer != nil && r.conn.buffer.IsHighWatermark() {
				if len(batchBuf) > 0 {
					r.flushBatch(&batchBuf)
					lastFlush = time.Now()
				}
			}

		case ev, ok := <-r.eventCh:
			if !ok {
				if len(batchBuf) > 0 {
					r.flushBatch(&batchBuf)
				}
				if r.conn.capture != nil {
					r.conn.capture.FinalizeForUpload()
				}
				r.sendDone()
				return
			}

			if ev == nil {
				continue
			}

			if lastReasoningType == "reasoning_only" && ev.Type != engine.EventThought {
				r.conn.detector.StartNewAttempt()
				lastReasoningType = ""
			}

			if ev.Type == engine.EventThought {
				lastReasoningType = "reasoning_only"
				if r.conn.capture != nil {
					if !captureStarted {
						captureStarted = true
						turnStartMs = time.Now().UnixMilli()
						r.conn.capture.BeginTurn(r.conn.sessionID, uint64(r.conn.detector.attemptCount+1))
					}
					r.conn.capture.StartGeneration(turnStartMs)
					if r.conn.capture.Phase != CaptureReasoning {
						r.conn.capture.Phase = CaptureReasoning
					}
				}
			}

			if ev.Type == engine.EventToolCall && r.conn.capture != nil {
				r.conn.capture.MarkToolCall()
			}

			batchBuf = append(batchBuf, ev)

			if r.conn.buffer != nil && r.conn.buffer.IsHighWatermark() {
				r.flushBatch(&batchBuf)
				lastFlush = time.Now()
				continue
			}

			if len(batchBuf) >= 16 {
				r.flushBatch(&batchBuf)
				lastFlush = time.Now()
			}
		}
	}
}

func (r *SSEStreamRunner) flushBatch(batch *[]*engine.Event) {
	for _, ev := range *batch {
		if ev == nil {
			continue
		}

		sseEvent := r.conn.ConvertEngineEvent(ev)
		if sseEvent == nil {
			continue
		}

		if r.conn.buffer != nil {
			if !r.conn.buffer.Send(sseEvent) {
				SSEObserveDrop()
				r.handler.pool.RecordDrop()
				continue
			}
		}

		if err := r.conn.WriteEvent(sseEvent); err != nil {
			observability.Warnf("sse write event: %v", err)
		}

		if r.conn.capture != nil {
			switch ev.Type {
			case engine.EventThought:
				if ev.Content != "" {
					r.conn.capture.PushReasoningDelta(ev.Content)
				}
			case engine.EventTextDelta:
				if ev.Content != "" {
					r.conn.capture.PushTextDelta(ev.Content)
				}
			case engine.EventToolCall:
				r.conn.capture.MarkToolCall()
			case engine.EventDone:
				r.conn.capture.SetFinishReason("stop")
			case engine.EventError:
				r.conn.capture.SetFinishReason("error")
			}
		}
	}
	_ = r.conn.Flush()
	*batch = (*batch)[:0]
}

func (r *SSEStreamRunner) sendDone() {
	if r.conn.capture != nil {
		if reason := r.conn.capture.FinishReason; reason == "" {
			r.conn.capture.SetFinishReason("stop")
		}
	}
	done := NewDoneEvent(r.conn.sessionID, r.conn.NextSeq())
	_ = r.conn.WriteEvent(done)
	_ = r.conn.Flush()
}

func NewStreamEventFromEngine(sessionID string, seq uint64, ev *engine.Event) *StructuredEvent {
	sseEv := &StructuredEvent{
		Seq:       seq,
		SessionID: sessionID,
		Timestamp: time.Now().UnixMilli(),
	}

	if ev == nil {
		sseEv.Type = EventSystem
		return sseEv
	}

	switch ev.Type {
	case engine.EventThought:
		sseEv.Type = EventReasoningDelta
		sseEv.Reasoning = &ReasoningMeta{Delta: ev.Content}
	case engine.EventTextDelta:
		sseEv.Type = EventTextDelta
		sseEv.Delta = ev.Content
	case engine.EventToolCall:
		sseEv.Type = EventToolCallDelta
		sseEv.Delta = ev.Content
		if ev.Data != nil {
			sseEv.Data = mustJSON(map[string]any{"args": ev.Data})
		}
	case engine.EventToolResult:
		sseEv.Type = EventToolResult
		sseEv.Content = ev.Content
	case engine.EventAnswer:
		sseEv.Type = EventAnswer
		sseEv.Content = ev.Content
	case engine.EventDone:
		sseEv.Type = EventDone
		sseEv.Completed = true
	case engine.EventError:
		sseEv.Type = EventError
		sseEv.Content = ev.Content
		sseEv.Completed = true
	default:
		sseEv.Type = EventSystem
		sseEv.Content = ev.Content
	}

	sseEv.Step = ev.Step
	sseEv.Completed = ev.Completed

	return sseEv
}

func ParseLastEventID(header string) (uint64, error) {
	if header == "" {
		return 0, nil
	}
	id, err := strconv.ParseUint(header, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Last-Event-ID: %s", header)
	}
	return id, nil
}
