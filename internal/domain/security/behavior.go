package security

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// BehaviorEvent represents a single tracked behavior event.
type BehaviorEvent struct {
	Time      time.Time      `json:"time"`
	SessionID string         `json:"sessionId"`
	Tool      string         `json:"tool"`
	Target    string         `json:"target"`
	Category  string         `json:"category"`
	Risk      BehaviorRisk   `json:"risk"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// BehaviorRisk classifies the risk level of a behavior.
type BehaviorRisk int

const (
	BehaviorNormal BehaviorRisk = iota
	BehaviorLow
	BehaviorMedium
	BehaviorHigh
	BehaviorCritical
)

func (r BehaviorRisk) String() string {
	switch r {
	case BehaviorNormal:
		return "normal"
	case BehaviorLow:
		return "low"
	case BehaviorMedium:
		return "medium"
	case BehaviorHigh:
		return "high"
	case BehaviorCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// AnomalyRecord captures a detected anomalous behavior with context.
type AnomalyRecord struct {
	Time      time.Time      `json:"time"`
	Type      AnomalyType    `json:"type"`
	SessionID string         `json:"sessionId"`
	Severity  BehaviorRisk   `json:"severity"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

// AnomalyType enumerates the kinds of anomalies the analyzer can detect.
type AnomalyType string

const (
	AnomalyRapidSensitiveAccess AnomalyType = "rapid_sensitive_access"
	AnomalyLargeScopeDeletion   AnomalyType = "large_scope_deletion"
	AnomalyNetworkEgressBurst   AnomalyType = "network_egress_burst"
	AnomalyCredentialAccess     AnomalyType = "credential_access"
	AnomalyCrossBoundaryAccess  AnomalyType = "cross_boundary_access"
	AnomalyHighRiskToolSequence AnomalyType = "high_risk_tool_sequence"
	AnomalyReadThenWriteJump    AnomalyType = "read_then_write_jump"
)

// BehaviorTracker maintains sliding windows of behavior events per session
// and detects anomalous access patterns that may indicate security issues.
type BehaviorTracker struct {
	mu sync.RWMutex

	// Per-session event storage
	sessions map[string]*sessionBehavior

	// Configurable parameters
	rapidAccessWindow     time.Duration
	rapidAccessThreshold  int
	deletionBurstWindow   time.Duration
	deletionBurstMax      int
	networkBurstWindow    time.Duration
	networkBurstMax       int
	sensitivePathPrefixes []string
}

type sessionBehavior struct {
	events     []BehaviorEvent
	anomalies  []AnomalyRecord
	lastAccess map[string]time.Time
	createdAt  time.Time
}

// NewBehaviorTracker creates a tracker with default thresholds.
func NewBehaviorTracker() *BehaviorTracker {
	return &BehaviorTracker{
		sessions:              make(map[string]*sessionBehavior),
		rapidAccessWindow:     5 * time.Minute,
		rapidAccessThreshold:  3,
		deletionBurstWindow:   10 * time.Minute,
		deletionBurstMax:      5,
		networkBurstWindow:    5 * time.Minute,
		networkBurstMax:       5,
		sensitivePathPrefixes: []string{".ssh", ".env", ".pem", "credentials", "secret", "wallet", "id_rsa"},
	}
}

// SetRapidAccessThreshold configures the rapid sensitive access detection.
func (b *BehaviorTracker) SetRapidAccessThreshold(window time.Duration, maxCount int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rapidAccessWindow = window
	b.rapidAccessThreshold = maxCount
}

// SetDeletionBurstThreshold configures the deletion burst detection.
func (b *BehaviorTracker) SetDeletionBurstThreshold(window time.Duration, maxCount int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletionBurstWindow = window
	b.deletionBurstMax = maxCount
}

// Track records a behavior event and runs anomaly detection.
func (b *BehaviorTracker) Track(event BehaviorEvent) []AnomalyRecord {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if event.Time.IsZero() {
		event.Time = now
	}

	sb, ok := b.sessions[event.SessionID]
	if !ok {
		sb = &sessionBehavior{
			events:     make([]BehaviorEvent, 0, 128),
			anomalies:  make([]AnomalyRecord, 0, 16),
			lastAccess: make(map[string]time.Time),
			createdAt:  now,
		}
		b.sessions[event.SessionID] = sb
	}

	sb.events = append(sb.events, event)
	sb.lastAccess[event.Tool+":"+event.Target] = now

	newAnomalies := b.detectAnomaliesLocked(sb, event)
	sb.anomalies = append(sb.anomalies, newAnomalies...)

	return newAnomalies
}

// detectAnomaliesLocked runs all anomaly detection rules on a session's events.
func (b *BehaviorTracker) detectAnomaliesLocked(sb *sessionBehavior, current BehaviorEvent) []AnomalyRecord {
	var anomalies []AnomalyRecord
	now := time.Now()

	// Clean old events beyond the longest window
	maxWindow := b.deletionBurstWindow
	if b.rapidAccessWindow > maxWindow {
		maxWindow = b.rapidAccessWindow
	}
	if b.networkBurstWindow > maxWindow {
		maxWindow = b.networkBurstWindow
	}
	cutoff := now.Add(-maxWindow)
	filtered := make([]BehaviorEvent, 0, len(sb.events))
	for _, e := range sb.events {
		if e.Time.After(cutoff) {
			filtered = append(filtered, e)
		}
	}
	sb.events = filtered

	// Rule 1: Rapid sensitive file access
	sensitiveCount := 0
	sensitivePaths := make([]string, 0)
	rapidCutoff := now.Add(-b.rapidAccessWindow)
	for _, e := range sb.events {
		if e.Time.Before(rapidCutoff) {
			continue
		}
		if b.isSensitivePath(e.Target) {
			sensitiveCount++
			sensitivePaths = append(sensitivePaths, e.Target)
		}
	}
	if sensitiveCount >= b.rapidAccessThreshold {
		anomalies = append(anomalies, AnomalyRecord{
			Time:      now,
			Type:      AnomalyRapidSensitiveAccess,
			SessionID: current.SessionID,
			Severity:  BehaviorHigh,
			Message:   "rapid access to sensitive files detected",
			Details: map[string]any{
				"count":     sensitiveCount,
				"window":    b.rapidAccessWindow.String(),
				"paths":     sensitivePaths,
				"threshold": b.rapidAccessThreshold,
			},
		})
	}

	// Rule 2: Large scope deletion burst
	deletionCount := 0
	deletionTargets := make([]string, 0)
	deletionCutoff := now.Add(-b.deletionBurstWindow)
	for _, e := range sb.events {
		if e.Time.Before(deletionCutoff) {
			continue
		}
		if isDeletionTool(e.Tool) {
			deletionCount++
			deletionTargets = append(deletionTargets, e.Target)
		}
	}
	if deletionCount >= b.deletionBurstMax {
		anomalies = append(anomalies, AnomalyRecord{
			Time:      now,
			Type:      AnomalyLargeScopeDeletion,
			SessionID: current.SessionID,
			Severity:  BehaviorCritical,
			Message:   "burst of deletion operations detected",
			Details: map[string]any{
				"count":   deletionCount,
				"window":  b.deletionBurstWindow.String(),
				"targets": deletionTargets,
			},
		})
	}

	// Rule 3: Network egress burst
	networkCount := 0
	networkTargets := make([]string, 0)
	networkCutoff := now.Add(-b.networkBurstWindow)
	for _, e := range sb.events {
		if e.Time.Before(networkCutoff) {
			continue
		}
		if isNetworkTool(e.Tool) {
			networkCount++
			networkTargets = append(networkTargets, e.Target)
		}
	}
	if networkCount >= b.networkBurstMax {
		anomalies = append(anomalies, AnomalyRecord{
			Time:      now,
			Type:      AnomalyNetworkEgressBurst,
			SessionID: current.SessionID,
			Severity:  BehaviorHigh,
			Message:   "rapid network egress operations detected",
			Details: map[string]any{
				"count":   networkCount,
				"window":  b.networkBurstWindow.String(),
				"targets": networkTargets,
			},
		})
	}

	// Rule 4: Credential access
	if b.isCredentialPath(current.Target) {
		// Check if accessed within a short window of write or network operations
		writeCutoff := now.Add(-2 * time.Minute)
		hasRecentWriteOrNetwork := false
		for _, e := range sb.events {
			if e.Time.Before(writeCutoff) {
				continue
			}
			if isWriteTool(e.Tool) || isNetworkTool(e.Tool) {
				hasRecentWriteOrNetwork = true
				break
			}
		}
		if hasRecentWriteOrNetwork {
			anomalies = append(anomalies, AnomalyRecord{
				Time:      now,
				Type:      AnomalyCredentialAccess,
				SessionID: current.SessionID,
				Severity:  BehaviorCritical,
				Message:   "credential file accessed near write/network operations",
				Details: map[string]any{
					"target": current.Target,
					"tool":   current.Tool,
				},
			})
		}
	}

	// Rule 5: Cross-boundary access (outside workspace)
	if isPathTraversal(current.Target) {
		anomalies = append(anomalies, AnomalyRecord{
			Time:      now,
			Type:      AnomalyCrossBoundaryAccess,
			SessionID: current.SessionID,
			Severity:  BehaviorHigh,
			Message:   "path traversal or cross-boundary access detected",
			Details: map[string]any{
				"target": current.Target,
				"tool":   current.Tool,
			},
		})
	}

	// Rule 6: Read-then-write jump (reads sensitive data then writes it elsewhere)
	if isWriteTool(current.Tool) {
		readCutoff := now.Add(-5 * time.Minute)
		for _, e := range sb.events {
			if e.Time.Before(readCutoff) {
				continue
			}
			if isReadTool(e.Tool) && b.isSensitivePath(e.Target) {
				anomalies = append(anomalies, AnomalyRecord{
					Time:      now,
					Type:      AnomalyReadThenWriteJump,
					SessionID: current.SessionID,
					Severity:  BehaviorHigh,
					Message:   "read sensitive data then immediately wrote to a new location",
					Details: map[string]any{
						"read_target":  e.Target,
						"write_target": current.Target,
						"read_tool":    e.Tool,
						"write_tool":   current.Tool,
					},
				})
				break
			}
		}
	}

	return anomalies
}

func (b *BehaviorTracker) isSensitivePath(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range b.sensitivePathPrefixes {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}

func (b *BehaviorTracker) isCredentialPath(path string) bool {
	lower := strings.ToLower(path)
	credentialPatterns := []string{".pem", ".key", ".p12", ".pfx", ".pub", "credentials", "id_rsa", "id_ed25519"}
	for _, pat := range credentialPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// GetAnomalies returns detected anomalies for a session.
func (b *BehaviorTracker) GetAnomalies(sessionID string, limit int) []AnomalyRecord {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sb, ok := b.sessions[sessionID]
	if !ok {
		return nil
	}
	result := make([]AnomalyRecord, 0, len(sb.anomalies))
	start := 0
	if limit > 0 && len(sb.anomalies) > limit {
		start = len(sb.anomalies) - limit
	}
	for i := start; i < len(sb.anomalies); i++ {
		result = append(result, sb.anomalies[i])
	}
	return result
}

// GetSessionRisk returns the highest risk level observed for a session.
func (b *BehaviorTracker) GetSessionRisk(sessionID string) BehaviorRisk {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sb, ok := b.sessions[sessionID]
	if !ok || len(sb.anomalies) == 0 {
		return BehaviorNormal
	}
	maxRisk := BehaviorNormal
	for _, a := range sb.anomalies {
		if a.Severity > maxRisk {
			maxRisk = a.Severity
		}
	}
	return maxRisk
}

// GetRecentEvents returns the most recent events for a session.
func (b *BehaviorTracker) GetRecentEvents(sessionID string, limit int) []BehaviorEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sb, ok := b.sessions[sessionID]
	if !ok {
		return nil
	}
	result := make([]BehaviorEvent, 0, limit)
	start := 0
	if limit > 0 && len(sb.events) > limit {
		start = len(sb.events) - limit
	}
	for i := start; i < len(sb.events); i++ {
		result = append(result, sb.events[i])
	}
	return result
}

// GetSessionSummary returns a summary of session behavior for risk assessment.
func (b *BehaviorTracker) GetSessionSummary(sessionID string) BehaviorSummary {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sb, ok := b.sessions[sessionID]
	if !ok {
		return BehaviorSummary{}
	}

	toolCounts := make(map[string]int)
	for _, e := range sb.events {
		toolCounts[e.Tool]++
	}

	type toolCount struct {
		Name  string
		Count int
	}
	var sortedTools []toolCount
	for name, count := range toolCounts {
		sortedTools = append(sortedTools, toolCount{Name: name, Count: count})
	}
	sort.Slice(sortedTools, func(i, j int) bool {
		return sortedTools[i].Count > sortedTools[j].Count
	})

	toolNames := make([]string, 0, len(sortedTools))
	for i, tc := range sortedTools {
		if i >= 10 {
			break
		}
		toolNames = append(toolNames, tc.Name)
	}

	return BehaviorSummary{
		SessionID:      sessionID,
		TotalEvents:    len(sb.events),
		AnomalyCount:   len(sb.anomalies),
		HighestRisk:    b.computeRiskLocked(sb),
		RecentTools:    toolNames,
		LastActivityAt: time.Now(),
	}
}

func (b *BehaviorTracker) computeRiskLocked(sb *sessionBehavior) BehaviorRisk {
	maxRisk := BehaviorNormal
	for _, a := range sb.anomalies {
		if a.Severity > maxRisk {
			maxRisk = a.Severity
		}
	}
	eventCount := len(sb.events)
	if eventCount > 200 {
		if maxRisk < BehaviorMedium {
			maxRisk = BehaviorMedium
		}
	}
	return maxRisk
}

// CleanupOldSessions removes sessions with no activity for the given duration.
func (b *BehaviorTracker) CleanupOldSessions(maxAge time.Duration) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for id, sb := range b.sessions {
		if sb.lastAccess == nil {
			delete(b.sessions, id)
			removed++
			continue
		}
		lastTime := sb.createdAt
		for _, e := range sb.events {
			if e.Time.After(lastTime) {
				lastTime = e.Time
			}
		}
		if lastTime.Before(cutoff) {
			delete(b.sessions, id)
			removed++
		}
	}
	return removed
}

// BehaviorSummary provides a high-level overview of a session's behavior.
type BehaviorSummary struct {
	SessionID      string       `json:"sessionId"`
	TotalEvents    int          `json:"totalEvents"`
	AnomalyCount   int          `json:"anomalyCount"`
	HighestRisk    BehaviorRisk `json:"highestRisk"`
	RecentTools    []string     `json:"recentTools"`
	LastActivityAt time.Time    `json:"lastActivityAt"`
}

func isDeletionTool(tool string) bool {
	base := strings.ToLower(tool)
	return strings.Contains(base, "delete") ||
		strings.Contains(base, "remove") ||
		strings.Contains(base, "rm") ||
		strings.Contains(base, "del") ||
		strings.Contains(base, "erase")
}

func isWriteTool(tool string) bool {
	base := strings.ToLower(tool)
	return strings.Contains(base, "write") ||
		strings.Contains(base, "edit") ||
		strings.Contains(base, "create") ||
		strings.Contains(base, "update") ||
		strings.Contains(base, "apply") ||
		strings.Contains(base, "patch")
}

func isReadTool(tool string) bool {
	base := strings.ToLower(tool)
	return strings.Contains(base, "read") ||
		strings.Contains(base, "search") ||
		strings.Contains(base, "glob") ||
		strings.Contains(base, "grep") ||
		strings.Contains(base, "find") ||
		strings.Contains(base, "list")
}

func isNetworkTool(tool string) bool {
	base := strings.ToLower(tool)
	return strings.Contains(base, "curl") ||
		strings.Contains(base, "wget") ||
		strings.Contains(base, "ssh") ||
		strings.Contains(base, "scp") ||
		strings.Contains(base, "nc") ||
		strings.Contains(base, "netcat") ||
		strings.Contains(base, "http") ||
		strings.Contains(base, "fetch")
}

func isPathTraversal(path string) bool {
	return strings.Contains(path, "../") || strings.Contains(path, "..\\") ||
		strings.HasPrefix(path, "/etc/") || strings.HasPrefix(path, "/root/")
}
