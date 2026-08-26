package eval

import (
	"sync"
	"time"
)

// Dimension identifies an evaluation dimension.
type Dimension string

const (
	DimAccuracy       Dimension = "accuracy"        // Task completion accuracy
	DimEfficiency     Dimension = "efficiency"      // Token cost, step count, latency
	DimSafety         Dimension = "safety"          // Permission denies, security incidents
	DimToolUsage      Dimension = "tool_usage"      // Tool call success/retry rates
	DimContextQuality Dimension = "context_quality" // Compression ratio, memory hit rate
	DimCost           Dimension = "cost"            // Estimated monetary cost
)

// SampleType classifies the kind of measurement.
type SampleType string

const (
	SampleCounter   SampleType = "counter"
	SampleGauge     SampleType = "gauge"
	SampleHistogram SampleType = "histogram"
	SampleBool      SampleType = "bool"
)

// Sample is a single evaluation measurement.
type Sample struct {
	Timestamp time.Time         `json:"ts"`
	Dimension Dimension         `json:"dim"`
	Name      string            `json:"name"`
	Type      SampleType        `json:"type"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// SessionMetrics holds aggregated metrics for a single agent session.
type SessionMetrics struct {
	SessionID  string    `json:"sessionId"`
	UserID     string    `json:"userId,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt,omitempty"`
	DurationMs int64     `json:"durationMs"`
	Completed  bool      `json:"completed"`
	ErrorClass string    `json:"errorClass,omitempty"`

	// Accuracy
	TaskCompleted bool `json:"taskCompleted"`
	TaskFailed    bool `json:"taskFailed"`

	// Efficiency
	TotalTokens   int   `json:"totalTokens"`
	InputTokens   int   `json:"inputTokens"`
	OutputTokens  int   `json:"outputTokens"`
	StepCount     int   `json:"stepCount"`
	ToolCallCount int   `json:"toolCallCount"`
	LLMCallCount  int   `json:"llmCallCount"`
	AvgLatencyMs  int64 `json:"avgLatencyMs"`

	// Safety
	PermissionDenies  int `json:"permissionDenies"`
	SecurityIncidents int `json:"securityIncidents"`

	// Tool Usage
	ToolSuccesses int            `json:"toolSuccesses"`
	ToolErrors    int            `json:"toolErrors"`
	ToolRetries   int            `json:"toolRetries"`
	ToolBreakdown map[string]int `json:"toolBreakdown,omitempty"`

	// Context Quality
	CompressionRatio float64 `json:"compressionRatio"`
	MemoryHits       int     `json:"memoryHits"`
	MemoryMisses     int     `json:"memoryMisses"`
	BlobOffloads     int     `json:"blobOffloads"`

	// Cost
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`

	// Topology
	Topology string `json:"topology"`

	// Samples collected during the session
	Samples []Sample `json:"samples,omitempty"`
}

// ScoreBreakdown holds per-dimension scores (0-100) with explanations.
type ScoreBreakdown struct {
	Scores       map[Dimension]float64 `json:"scores"`
	Explanations map[Dimension]string  `json:"explanations"`
	Overall      float64               `json:"overall"`
	Grade        string                `json:"grade"`
}

// EvalConfig controls evaluation behavior.
type EvalConfig struct {
	// Enabled turns on data collection (runtime overhead).
	Enabled bool
	// CollectSamples keeps raw samples in SessionMetrics.Samples.
	CollectSamples bool
	// MaxSamplesPerSession limits memory per session.
	MaxSamplesPerSession int
	// CostPer1KInputTokens USD cost per 1K input tokens (default: $0.003).
	CostPer1KInputTokens float64
	// CostPer1KOutputTokens USD cost per 1K output tokens (default: $0.015).
	CostPer1KOutputTokens float64
}

// DefaultEvalConfig returns sensible defaults.
func DefaultEvalConfig() EvalConfig {
	return EvalConfig{
		Enabled:               true,
		CollectSamples:        false,
		MaxSamplesPerSession:  500,
		CostPer1KInputTokens:  0.003,
		CostPer1KOutputTokens: 0.015,
	}
}

// Collector gathers evaluation metrics for agent sessions.
// Thread-safe; safe for nil receiver (no-op when disabled).
type Collector struct {
	mu       sync.Mutex
	cfg      EvalConfig
	sessions map[string]*SessionMetrics
}

// NewCollector creates a new evaluation collector.
func NewCollector(cfg EvalConfig) *Collector {
	return &Collector{
		cfg:      cfg,
		sessions: map[string]*SessionMetrics{},
	}
}

// DefaultCollector is the process-wide collector.
var DefaultCollector = NewCollector(DefaultEvalConfig())

// SetConfig updates the collector configuration.
func (c *Collector) SetConfig(cfg EvalConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
}

// BeginSession starts metrics collection for a session.
func (c *Collector) BeginSession(sessionID, userID string) *SessionMetrics {
	if c == nil || !c.cfg.Enabled {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m := &SessionMetrics{
		SessionID: sessionID,
		UserID:    userID,
		StartedAt: time.Now(),
	}
	c.sessions[sessionID] = m
	return m
}

// EndSession marks a session as ended and computes final metrics.
func (c *Collector) EndSession(sessionID string, completed bool, errorClass string) *SessionMetrics {
	if c == nil || !c.cfg.Enabled {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.sessions[sessionID]
	if !ok {
		return nil
	}
	m.EndedAt = time.Now()
	m.DurationMs = m.EndedAt.Sub(m.StartedAt).Milliseconds()
	m.Completed = completed
	m.ErrorClass = errorClass
	if completed {
		m.TaskCompleted = true
	} else {
		m.TaskFailed = true
	}
	if m.LLMCallCount > 0 {
		m.AvgLatencyMs = m.DurationMs / int64(m.LLMCallCount)
	}
	// Estimate cost.
	if m.InputTokens == 0 && m.OutputTokens == 0 && m.TotalTokens > 0 {
		m.InputTokens = m.TotalTokens / 2
		m.OutputTokens = m.TotalTokens - m.InputTokens
	}
	inputCost := float64(m.InputTokens) / 1000 * c.cfg.CostPer1KInputTokens
	outputCost := float64(m.OutputTokens) / 1000 * c.cfg.CostPer1KOutputTokens
	m.EstimatedCostUSD = inputCost + outputCost
	delete(c.sessions, sessionID)
	return m
}

// AddSample records a single measurement for a session.
func (c *Collector) AddSample(sessionID string, s Sample) {
	if c == nil || !c.cfg.Enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.sessions[sessionID]
	if !ok {
		return
	}
	s.Timestamp = time.Now()
	if c.cfg.CollectSamples {
		if len(m.Samples) >= c.cfg.MaxSamplesPerSession {
			m.Samples = m.Samples[1:]
		}
		m.Samples = append(m.Samples, s)
	}
	// Update aggregated fields based on dimension.
	inc := int(s.Value)
	switch {
	case s.Dimension == DimAccuracy && s.Name == "task_completed" && s.Type == SampleBool:
		m.TaskCompleted = s.Value > 0.5
	case s.Dimension == DimEfficiency:
		switch s.Name {
		case "tokens_total":
			m.TotalTokens = inc
		case "tokens_input":
			m.InputTokens = inc
		case "tokens_output":
			m.OutputTokens = inc
		case "step_count":
			m.StepCount = inc
		case "tool_calls":
			m.ToolCallCount = inc
		case "llm_calls":
			m.LLMCallCount = inc
		}
	case s.Dimension == DimSafety:
		switch s.Name {
		case "permission_deny":
			m.PermissionDenies += maxInt(inc, 1)
		case "security_incident":
			m.SecurityIncidents += maxInt(inc, 1)
		}
	case s.Dimension == DimToolUsage:
		switch s.Name {
		case "tool_success":
			m.ToolSuccesses += maxInt(inc, 1)
		case "tool_error":
			m.ToolErrors += maxInt(inc, 1)
		case "tool_retry":
			m.ToolRetries += maxInt(inc, 1)
		}
	case s.Dimension == DimContextQuality:
		switch s.Name {
		case "compression_ratio":
			m.CompressionRatio = s.Value
		case "memory_hit":
			m.MemoryHits += maxInt(inc, 1)
		case "memory_miss":
			m.MemoryMisses += maxInt(inc, 1)
		case "blob_offload":
			m.BlobOffloads += maxInt(inc, 1)
		}
	case s.Dimension == DimCost:
		switch s.Name {
		case "estimated_cost":
			m.EstimatedCostUSD = s.Value
		}
	}
}

// SetTopology records the agent topology used for the session.
func (c *Collector) SetTopology(sessionID, topology string) {
	if c == nil || !c.cfg.Enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.sessions[sessionID]; ok {
		m.Topology = topology
	}
}

// AddToolBreakdown adds tool-level breakdown counters.
func (c *Collector) AddToolBreakdown(sessionID, toolName string, success bool) {
	if c == nil || !c.cfg.Enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.sessions[sessionID]
	if !ok {
		return
	}
	if m.ToolBreakdown == nil {
		m.ToolBreakdown = map[string]int{}
	}
	m.ToolBreakdown[toolName]++
	if success {
		m.ToolSuccesses++
	} else {
		m.ToolErrors++
	}
}

// GetSnapshot returns a copy of the current metrics for a session.
func (c *Collector) GetSnapshot(sessionID string) *SessionMetrics {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.sessions[sessionID]
	if !ok {
		return nil
	}
	cp := *m
	return &cp
}

// ActiveSessions returns the number of sessions being tracked.
func (c *Collector) ActiveSessions() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sessions)
}

// maxInt returns the larger of a and b.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Compile-time check that DefaultCollector is usable.
var _ = DefaultCollector
