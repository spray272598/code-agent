package orchestration

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Blackboard enables lightweight inter-agent communication (P2-1).
// Sub-agents write key findings (file paths, function names, errors) that
// become visible to other sub-agents via their system prompt.
type Blackboard struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// Entry is a single blackboard record.
type Entry struct {
	Key       string        `json:"key"`
	Agent     string        `json:"agent"`
	Value     interface{}   `json:"value"`
	Timestamp time.Time     `json:"ts"`
	TTL       time.Duration `json:"ttl,omitempty"`
}

// NewBlackboard returns an empty Blackboard.
func NewBlackboard() *Blackboard {
	return &Blackboard{entries: map[string]*Entry{}}
}

// Write sets (or overwrites) a key from an agent.
func (b *Blackboard) Write(key, agent string, value interface{}) {
	b.WriteWithTTL(key, agent, value, 0)
}

// WriteWithTTL sets a key with a time-to-live.
func (b *Blackboard) WriteWithTTL(key, agent string, value interface{}, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[key] = &Entry{
		Key:       key,
		Agent:     agent,
		Value:     value,
		Timestamp: time.Now(),
		TTL:       ttl,
	}
}

// Read returns the value for a key (nil if missing or expired).
func (b *Blackboard) Read(key string) (interface{}, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.entries[key]
	if !ok {
		return nil, false
	}
	if e.TTL > 0 && time.Since(e.Timestamp) > e.TTL {
		return nil, false
	}
	return e.Value, true
}

// Delete removes a key.
func (b *Blackboard) Delete(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

// Snapshot returns a copy of all valid entries (excluding expired).
func (b *Blackboard) Snapshot() map[string]Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := map[string]Entry{}
	now := time.Now()
	for k, e := range b.entries {
		if e.TTL > 0 && now.Sub(e.Timestamp) > e.TTL {
			continue
		}
		out[k] = *e
	}
	return out
}

// Summary renders the blackboard as a compact string that can be injected into prompts.
func (b *Blackboard) Summary() string {
	snap := b.Snapshot()
	if len(snap) == 0 {
		return ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var lines []string
	lines = append(lines, "## Blackboard (shared findings)")
	for k, e := range snap {
		v, _ := json.Marshal(e.Value)
		lines = append(lines, fmt.Sprintf("- %s: %s", k, v))
	}
	var out strings.Builder
	for i, l := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(l)
	}
	return out.String()
}

// Clear removes all entries.
func (b *Blackboard) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = map[string]*Entry{}
}

// Size returns the number of entries (including possibly-expired).
func (b *Blackboard) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

// Common blackboard keys --------------------------------------------------

const (
	BBKeyFilePath   = "file.path"
	BBKeyFunction   = "function.found"
	BBKeyError      = "error.found"
	BBKeyMetric     = "metric.found"
	BBKeyPlanStep   = "plan.step"
	BBKeyRisk       = "risk.found"
	BBKeyTestResult = "test.result"
)

// Predefined keys for structured entries.
var PredefinedKeys = []string{
	BBKeyFilePath, BBKeyFunction, BBKeyError, BBKeyMetric,
	BBKeyPlanStep, BBKeyRisk, BBKeyTestResult,
}

// WriteFileRecord records a discovered file path.
func (b *Blackboard) WriteFileRecord(agent, path string) {
	b.Write(BBKeyFilePath+"."+sanitizeKey(path), agent, path)
}

// WriteFunctionRecord records a discovered function name.
func (b *Blackboard) WriteFunctionRecord(agent, name, file string) {
	b.Write(BBKeyFunction+"."+sanitizeKey(name), agent, map[string]string{"name": name, "file": file})
}

// WriteErrorRecord records a discovered error.
func (b *Blackboard) WriteErrorRecord(agent, msg, stack string) {
	b.Write(BBKeyError+"."+sanitizeKey(msg), agent, map[string]string{"msg": msg, "stack": stack})
}

// WriteTestResult records a test outcome.
func (b *Blackboard) WriteTestResult(agent, name string, passed bool, durationMs int64) {
	status := "pass"
	if !passed {
		status = "fail"
	}
	b.Write(BBKeyTestResult+"."+sanitizeKey(name), agent,
		map[string]interface{}{"test": name, "status": status, "duration_ms": durationMs})
}

func sanitizeKey(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '/' || c == '\\' || c == '.' || c == '-':
			out = append(out, '_')
		default:
			out = append(out, '_')
		}
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return string(out)
}
