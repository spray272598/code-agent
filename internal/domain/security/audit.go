package security

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type AuditLevel string

const (
	AuditViolation AuditLevel = "violation"
	AuditWarning   AuditLevel = "warning"
	AuditInfo      AuditLevel = "info"
)

type AuditCategory string

const (
	CategorySandbox AuditCategory = "sandbox"
	CategoryTool    AuditCategory = "tool"
	CategoryAuth    AuditCategory = "auth"
	CategoryNetwork AuditCategory = "network"
	CategoryDenied  AuditCategory = "denied"
	CategoryConfirm AuditCategory = "confirm"
	CategorySystem  AuditCategory = "system"
)

type AuditEvent struct {
	Time      time.Time      `json:"time"`
	Level     AuditLevel     `json:"level"`
	Category  AuditCategory  `json:"category"`
	RuleID    string         `json:"ruleId,omitempty"`
	Target    string         `json:"target"`
	Detail    string         `json:"detail"`
	SessionID string         `json:"sessionId,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

type AuditMetrics struct {
	Violations  int64 `json:"violations"`
	Confirms    int64 `json:"confirms"`
	Denies      int64 `json:"denies"`
	Allows      int64 `json:"allows"`
	LastEventAt int64 `json:"lastEventAt"`
}

type AuditLogger struct {
	mu      sync.Mutex
	file    *os.File
	buffer  []AuditEvent
	maxBuf  int
	enabled bool
	flushOn bool
	maxSize int64
	size    int64
	metrics AuditMetrics
	logger  *log.Logger
}

var (
	defaultAudit *AuditLogger
	auditOnce    sync.Once
)

func DefaultAuditLogger() *AuditLogger {
	auditOnce.Do(func() {
		defaultAudit = NewAuditLogger(AuditConfig{
			Enabled:      true,
			LogPath:      "audit.log",
			MaxSizeMB:    100,
			FlushOnWrite: true,
		})
	})
	return defaultAudit
}

func NewAuditLogger(cfg AuditConfig) *AuditLogger {
	a := &AuditLogger{
		buffer:  make([]AuditEvent, 0, 256),
		maxBuf:  256,
		enabled: cfg.Enabled,
		flushOn: cfg.FlushOnWrite,
		maxSize: int64(cfg.MaxSizeMB) * 1024 * 1024,
		logger:  log.New(os.Stderr, "[AUDIT] ", log.LstdFlags),
	}
	if cfg.LogPath != "" {
		f, err := os.OpenFile(cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			a.file = f
		} else {
			a.logger.Printf("audit log file open failed: %v", err)
		}
	}
	return a
}

func (a *AuditLogger) SetEnabled(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enabled = v
}

func (a *AuditLogger) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		a.flushLocked()
		a.file.Close()
		a.file = nil
	}
}

func (a *AuditLogger) Violation(category AuditCategory, ruleID, target, detail string, sessionID ...string) {
	a.event(AuditViolation, category, ruleID, target, detail, sessionID...)
	atomic.AddInt64(&a.metrics.Violations, 1)
	atomic.StoreInt64(&a.metrics.LastEventAt, time.Now().Unix())
}

func (a *AuditLogger) Deny(category AuditCategory, ruleID, target, detail string, sessionID ...string) {
	a.event(AuditViolation, category, ruleID, target, detail, sessionID...)
	atomic.AddInt64(&a.metrics.Denies, 1)
	atomic.StoreInt64(&a.metrics.LastEventAt, time.Now().Unix())
}

func (a *AuditLogger) Confirm(category AuditCategory, ruleID, target, detail string, sessionID ...string) {
	a.event(AuditWarning, category, ruleID, target, detail, sessionID...)
	atomic.AddInt64(&a.metrics.Confirms, 1)
	atomic.StoreInt64(&a.metrics.LastEventAt, time.Now().Unix())
}

func (a *AuditLogger) Allow(category AuditCategory, target, detail string, sessionID ...string) {
	a.event(AuditInfo, category, "", target, detail, sessionID...)
	atomic.AddInt64(&a.metrics.Allows, 1)
	atomic.StoreInt64(&a.metrics.LastEventAt, time.Now().Unix())
}

func (a *AuditLogger) Warn(category AuditCategory, target, detail string) {
	a.event(AuditWarning, category, "", target, detail)
}

func (a *AuditLogger) Info(category AuditCategory, target, detail string) {
	a.event(AuditInfo, category, "", target, detail)
}

func (a *AuditLogger) event(level AuditLevel, category AuditCategory, ruleID, target, detail string, sessionID ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.enabled {
		return
	}

	sid := ""
	if len(sessionID) > 0 {
		sid = sessionID[0]
	}

	evt := AuditEvent{
		Time:      time.Now(),
		Level:     level,
		Category:  category,
		RuleID:    ruleID,
		Target:    target,
		Detail:    detail,
		SessionID: sid,
	}

	a.buffer = append(a.buffer, evt)
	if len(a.buffer) >= a.maxBuf || a.flushOn {
		a.flushLocked()
	}
}

func (a *AuditLogger) Flush() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flushLocked()
}

func (a *AuditLogger) flushLocked() {
	if len(a.buffer) == 0 {
		return
	}
	data, _ := json.Marshal(a.buffer)
	if a.file != nil {
		written, _ := a.file.Write(append(data, '\n'))
		a.size += int64(written)
		if a.size > a.maxSize {
			a.rotateLocked()
		}
	}
	a.logger.Printf("%s: %s/%s: %s - %s",
		a.buffer[0].Level, a.buffer[0].Category,
		a.buffer[0].RuleID, a.buffer[0].Target, a.buffer[0].Detail)
	a.buffer = a.buffer[:0]
}

func (a *AuditLogger) rotateLocked() {
	if a.file != nil {
		a.file.Close()
		backupPath := a.file.Name() + "." + time.Now().Format("20060102-150405")
		os.Rename(a.file.Name(), backupPath)
		newFile, err := os.OpenFile(a.file.Name(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			a.file = newFile
		}
	}
	a.size = 0
}

func (a *AuditLogger) Metrics() AuditMetrics {
	return AuditMetrics{
		Violations:  atomic.LoadInt64(&a.metrics.Violations),
		Confirms:    atomic.LoadInt64(&a.metrics.Confirms),
		Denies:      atomic.LoadInt64(&a.metrics.Denies),
		Allows:      atomic.LoadInt64(&a.metrics.Allows),
		LastEventAt: atomic.LoadInt64(&a.metrics.LastEventAt),
	}
}

func (a *AuditLogger) Events(category AuditCategory, limit int) []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AuditEvent
	for i := len(a.buffer) - 1; i >= 0 && len(out) < limit; i-- {
		if category == "" || a.buffer[i].Category == category {
			out = append(out, a.buffer[i])
		}
	}
	return out
}

func (a *AuditLogger) SetLogPath(path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		a.file.Close()
	}
	dir := filepath.Dir(path)
	if dir != "." {
		os.MkdirAll(dir, 0o750)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	a.file = f
	return nil
}
