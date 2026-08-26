package sse

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultDoomTailRepeatThreshold = 8
	DefaultDoomRepeatWindow        = 20
	DefaultDoomMaxReasoningTokens  = 32000
	DefaultDoomMaxTurnAttempts     = 10
	DefaultDoomMaxStreamBytes     = 8 * 1024 * 1024
)

type DoomLoopTrigger struct {
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
	ChunkIdx  int    `json:"chunkIdx,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type DoomLoopSegmentStamp struct {
	Triggers         []DoomLoopTrigger `json:"triggers"`
	Attempt          int               `json:"attempt"`
	AbortedAtChunk   int               `json:"abortedAtChunk,omitempty"`
	Action           string            `json:"action"`
}

type DoomLoopDetector struct {
	mu              sync.Mutex
	tailRepeatCount int
	repeatWindow    int
	maxReasoning    int
	maxAttempts     int
	maxStreamBytes  int64
	reasoningTokens int
	attemptCount    int
	chunkCount      int
	streamBytes     int64
	lastDelta       string
	consecutiveSame int
	stamps          []DoomLoopSegmentStamp
	aborted         bool
	abortReason     string
	lastCheck       time.Time
}

func NewDoomLoopDetector() *DoomLoopDetector {
	return &DoomLoopDetector{
		repeatWindow:   DefaultDoomRepeatWindow,
		maxReasoning:   DefaultDoomMaxReasoningTokens,
		maxAttempts:    DefaultDoomMaxTurnAttempts,
		maxStreamBytes: DefaultDoomMaxStreamBytes,
	}
}

func (d *DoomLoopDetector) RecordDelta(delta string, reasoning bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.chunkCount++
	d.streamBytes += int64(len(delta))
	d.lastCheck = time.Now()

	if reasoning {
		d.reasoningTokens += estimateTokens(delta)
	}

	if delta == d.lastDelta && delta != "" {
		d.consecutiveSame++
		if d.consecutiveSame >= DefaultDoomTailRepeatThreshold {
			d.flagTailRepeat(reasoning)
		}
	} else {
		d.consecutiveSame = 0
	}
	d.lastDelta = delta

	if d.reasoningTokens > d.maxReasoning {
		d.flagReasoningBudgetExceeded()
	}
	if d.streamBytes > d.maxStreamBytes {
		d.flagStreamByteLimitExceeded()
	}
}

func (d *DoomLoopDetector) StartNewAttempt() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attemptCount++
	d.reasoningTokens = 0
	d.consecutiveSame = 0
	d.lastDelta = ""
}

func (d *DoomLoopDetector) StampAction(action string, trigger DoomLoopTrigger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stampActionLocked(action, trigger)
}

func (d *DoomLoopDetector) stampActionLocked(action string, trigger DoomLoopTrigger) {
	d.stamps = append(d.stamps, DoomLoopSegmentStamp{
		Triggers:       []DoomLoopTrigger{trigger},
		Attempt:        d.attemptCount,
		AbortedAtChunk: d.chunkCount,
		Action:         action,
	})
}

func (d *DoomLoopDetector) IsDoomed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.aborted
}

func (d *DoomLoopDetector) AbortReason() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.abortReason
}

func (d *DoomLoopDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tailRepeatCount = 0
	d.reasoningTokens = 0
	d.attemptCount = 0
	d.chunkCount = 0
	d.streamBytes = 0
	d.lastDelta = ""
	d.consecutiveSame = 0
	d.stamps = nil
	d.aborted = false
	d.abortReason = ""
}

func (d *DoomLoopDetector) Stats() map[string]interface{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return map[string]interface{}{
		"attemptCount":    d.attemptCount,
		"chunkCount":      d.chunkCount,
		"reasoningTokens": d.reasoningTokens,
		"streamBytes":     d.streamBytes,
		"aborted":         d.aborted,
		"abortReason":     d.abortReason,
	}
}

func (d *DoomLoopDetector) Stamps() []DoomLoopSegmentStamp {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]DoomLoopSegmentStamp, len(d.stamps))
	copy(result, d.stamps)
	return result
}

func (d *DoomLoopDetector) flagTailRepeat(reasoning bool) {
	name := "tail_repetition"
	if reasoning {
		name += "_thinking"
	}
	trigger := DoomLoopTrigger{
		Name:      name,
		Detail:    strconv.Itoa(d.consecutiveSame) + "@thinking",
		ChunkIdx: d.chunkCount,
		Timestamp: time.Now().UnixMilli(),
	}
	d.aborted = true
	d.abortReason = "tail_repetition:" + name
	d.stampActionLocked("resampled", trigger)
}

func (d *DoomLoopDetector) flagReasoningBudgetExceeded() {
	trigger := DoomLoopTrigger{
		Name:      "reasoning_budget_exceeded",
		Detail:    "tokens=" + strconv.Itoa(d.reasoningTokens),
		Timestamp: time.Now().UnixMilli(),
	}
	d.aborted = true
	d.abortReason = "reasoning_budget_exceeded"
	d.stampActionLocked("accepted_after_budget", trigger)
}

func (d *DoomLoopDetector) flagStreamByteLimitExceeded() {
	trigger := DoomLoopTrigger{
		Name:      "stream_byte_limit",
		Detail:    "bytes=" + strconv.FormatInt(d.streamBytes, 10),
		Timestamp: time.Now().UnixMilli(),
	}
	d.aborted = true
	d.abortReason = "stream_byte_limit"
	d.stampActionLocked("accepted_after_budget", trigger)
}

func estimateTokens(s string) int {
	return len([]rune(s)) / 4
}

type CapturePhase int

const (
	CaptureReasoning CapturePhase = iota
	CaptureResponseText
	CaptureToolCall
	CapturePhaseDefault
)

func (p CapturePhase) String() string {
	switch p {
	case CaptureReasoning:
		return "reasoning"
	case CaptureResponseText:
		return "response_text"
	case CaptureToolCall:
		return "tool_call"
	default:
		return "idle"
	}
}

type StreamSegment struct {
	StartedAtMs       int64                `json:"startedAtMs,omitempty"`
	ReasoningText     string               `json:"reasoningText"`
	ResponseText      string               `json:"responseText"`
	ReasoningChunks   int                  `json:"reasoningChunks"`
	TextChunks        int                  `json:"textChunks"`
	Phase             CapturePhase         `json:"phase"`
	DoomLoop          *DoomLoopSegmentStamp `json:"doomLoop,omitempty"`
}

type StreamingTurnCapture struct {
	PromptID        string              `json:"promptId,omitempty"`
	TurnNumber      uint64              `json:"turnNumber"`
	StartedAtMs     int64               `json:"startedAtMs,omitempty"`
	ReasoningText   string              `json:"reasoningText"`
	ResponseText    string              `json:"responseText"`
	ReasoningChunks int                 `json:"reasoningChunks"`
	TextChunks      int                 `json:"textChunks"`
	Truncated       bool                `json:"truncated"`
	Reason          string              `json:"reason,omitempty"`
	Phase           CapturePhase        `json:"phase"`
	Segments        []StreamSegment     `json:"segments"`
	AttemptCount    int                 `json:"attemptCount"`
	ReasoningTokens int                 `json:"reasoningTokens,omitempty"`
	CompletionTokens int                `json:"completionTokens,omitempty"`
	FinishReason    string              `json:"finishReason,omitempty"`
	EmptyReason     string              `json:"emptyReason,omitempty"`
	DoomLoop        *DoomLoopSegmentStamp `json:"doomLoop,omitempty"`

	maxBytes int
}

func NewStreamingTurnCapture(maxBytes int) *StreamingTurnCapture {
	return &StreamingTurnCapture{
		maxBytes: maxBytes,
	}
}

func (c *StreamingTurnCapture) BeginTurn(promptID string, turnNumber uint64) {
	*c = StreamingTurnCapture{
		maxBytes: c.maxBytes,
	}
	c.PromptID = promptID
	c.TurnNumber = turnNumber
}

func (c *StreamingTurnCapture) StartGeneration(startedAtMs int64) {
	c.pushCurrentSegment()
	c.StartedAtMs = startedAtMs
	c.AttemptCount++
	c.Phase = CaptureReasoning
}

func (c *StreamingTurnCapture) PushReasoningDelta(delta string) {
	c.appendDelta(true, delta)
}

func (c *StreamingTurnCapture) PushTextDelta(delta string) {
	c.appendDelta(false, delta)
}

func (c *StreamingTurnCapture) MarkToolCall() {
	c.Phase = CaptureToolCall
}

func (c *StreamingTurnCapture) SetFinishReason(reason string) {
	c.FinishReason = reason
}

func (c *StreamingTurnCapture) SetEmptyReason(reason string) {
	c.EmptyReason = reason
}

func (c *StreamingTurnCapture) SetTokenCounts(reasoning, completion int) {
	c.ReasoningTokens = reasoning
	c.CompletionTokens = completion
}

func (c *StreamingTurnCapture) IsEmpty() bool {
	return c.ReasoningText == "" &&
		c.ResponseText == "" &&
		len(c.Segments) == 0 &&
		c.ReasoningTokens == 0 &&
		c.CompletionTokens == 0 &&
		c.FinishReason == "" &&
		c.EmptyReason == ""
}

func (c *StreamingTurnCapture) FinalizeForUpload() {
	c.pushCurrentSegment()
	c.Truncated = c.totalBytes() >= c.maxBytes

	var reasoning, response strings.Builder
	var reasoningChunks, textChunks int
	for i, seg := range c.Segments {
		appendAttempt(&reasoning, i+1, seg.ReasoningText)
		appendAttempt(&response, i+1, seg.ResponseText)
		reasoningChunks += seg.ReasoningChunks
		textChunks += seg.TextChunks
	}
	c.ReasoningText = reasoning.String()
	c.ResponseText = response.String()
	c.ReasoningChunks = reasoningChunks
	c.TextChunks = textChunks
	if len(c.Segments) > 0 {
		c.StartedAtMs = c.Segments[0].StartedAtMs
		c.Phase = c.Segments[len(c.Segments)-1].Phase
	}
}

func (c *StreamingTurnCapture) HasDoomLoopSegments() bool {
	if c.DoomLoop != nil {
		return true
	}
	for _, seg := range c.Segments {
		if seg.DoomLoop != nil {
			return true
		}
	}
	return false
}

func (c *StreamingTurnCapture) appendDelta(isReasoning bool, delta string) {
	if c.Truncated {
		return
	}
	total := c.totalBytes()
	if total >= c.maxBytes {
		c.Truncated = true
		return
	}
	remaining := c.maxBytes - total
	toAppend := delta
	if len(delta) > remaining {
		c.Truncated = true
		toAppend = delta[:remaining]
		for len(toAppend) > 0 && !isRuneBoundary(delta, len(toAppend)) {
			toAppend = toAppend[:len(toAppend)-1]
		}
	}

	if isReasoning {
		c.ReasoningText += toAppend
		c.ReasoningChunks++
		c.Phase = CaptureReasoning
	} else {
		c.ResponseText += toAppend
		c.TextChunks++
		c.Phase = CaptureResponseText
	}
}

func (c *StreamingTurnCapture) totalBytes() int {
	var segBytes int
	for _, s := range c.Segments {
		segBytes += len(s.ReasoningText) + len(s.ResponseText)
	}
	return len(c.ReasoningText) + len(c.ResponseText) + segBytes
}

func (c *StreamingTurnCapture) pushCurrentSegment() {
	if c.ReasoningText == "" && c.ResponseText == "" && c.DoomLoop == nil {
		return
	}

	seg := StreamSegment{
		StartedAtMs:     c.StartedAtMs,
		ReasoningText:   c.ReasoningText,
		ResponseText:    c.ResponseText,
		ReasoningChunks: c.ReasoningChunks,
		TextChunks:      c.TextChunks,
		Phase:           c.Phase,
		DoomLoop:        c.DoomLoop,
	}
	c.Segments = append(c.Segments, seg)

	c.ReasoningText = ""
	c.ResponseText = ""
	c.ReasoningChunks = 0
	c.TextChunks = 0
	c.Phase = CapturePhaseDefault
	c.DoomLoop = nil
}

func appendAttempt(buf *strings.Builder, idx int, text string) {
	if text == "" {
		return
	}
	if buf.Len() > 0 {
		buf.WriteString("\n\n--- attempt ")
		buf.WriteString(strconv.Itoa(idx))
		buf.WriteString(" ---\n\n")
	}
	buf.WriteString(text)
}

func isRuneBoundary(s string, pos int) bool {
	if pos <= 0 || pos >= len(s) {
		return true
	}
	b := s[pos]
	return b < 0x80 || b&0xC0 == 0xC0
}
