package security

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// InjectionSeverity classifies detected injection attempts.
type InjectionSeverity int

const (
	InjectionLow      InjectionSeverity = iota
	InjectionMedium
	InjectionHigh
	InjectionCritical
)

func (s InjectionSeverity) String() string {
	switch s {
	case InjectionLow:
		return "low"
	case InjectionMedium:
		return "medium"
	case InjectionHigh:
		return "high"
	case InjectionCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// InjectionMatch represents a single injection pattern match.
type InjectionMatch struct {
	Pattern  string            `json:"pattern"`
	Severity InjectionSeverity `json:"severity"`
	Category string            `json:"category"`
	Position int               `json:"position"`
	Context  string            `json:"context"`
}

// InjectionReport summarizes all detected injection attempts in a text.
type InjectionReport struct {
	Detected bool             `json:"detected"`
	Score    float64          `json:"score"`
	Matches  []InjectionMatch `json:"matches"`
	InputLen int              `json:"inputLen"`
	CheckedAt time.Time       `json:"checkedAt"`
}

// PromptInjectionDetector performs real-time semantic analysis of text
// (user input, tool outputs, file contents) for prompt injection attempts.
type PromptInjectionDetector struct {
	mu sync.RWMutex

	// Pattern categories: each category maps to a list of regex patterns
	// and a base severity score.
	categories map[string]injectionCategory

	// Historical detection statistics per session
	sessionStats map[string]*sessionInjectionStats

	// Configurable thresholds
	maxScore     float64
	autoBlock    InjectionSeverity
}

type injectionCategory struct {
	name      string
	patterns  []*regexp.Regexp
	severity  InjectionSeverity
	weight    float64
	enabled   bool
}

type sessionInjectionStats struct {
	totalChecks    int
	totalDetections int
	criticalCount  int
	highCount      int
	mediumCount    int
	lastCheckAt    time.Time
}

// NewPromptInjectionDetector creates a detector with default patterns for
// common injection categories.
func NewPromptInjectionDetector() *PromptInjectionDetector {
	d := &PromptInjectionDetector{
		categories:   make(map[string]injectionCategory),
		sessionStats: make(map[string]*sessionInjectionStats),
		maxScore:     0.7,
		autoBlock:    InjectionHigh,
	}
	d.initDefaultPatterns()
	return d
}

func (d *PromptInjectionDetector) initDefaultPatterns() {
	d.categories["role_override"] = injectionCategory{
		name:     "role_override",
		severity: InjectionCritical,
		weight:   3.0,
		enabled:  true,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\byou are now\b.*\b(admin|root|superuser|owner)\b`),
			regexp.MustCompile(`(?i)\bignore (all )?(previous|prior|above) (instructions?|rules?|prompts?)\b`),
			regexp.MustCompile(`(?i)\bforget (all )?(previous|prior|above) (instructions?|rules?|prompts?)\b`),
			regexp.MustCompile(`(?i)\bdisregard (all )?(previous|prior|above)\b`),
			regexp.MustCompile(`(?i)\bnow you are\b`),
			regexp.MustCompile(`(?i)\bact as (if|a|an)\b.*\b(hacker|attacker|pentester)\b`),
			regexp.MustCompile(`(?i)\bsecurity mode\b|\bdebug mode\b|\bfull access\b`),
		},
	}

	d.categories["tool_manipulation"] = injectionCategory{
		name:     "tool_manipulation",
		severity: InjectionHigh,
		weight:   2.5,
		enabled:  true,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bexecute (all )?(commands?|code|scripts?)\b`),
			regexp.MustCompile(`(?i)\brun (unauthorized|arbitrary)\b.*\b(commands?|code|shell)\b`),
			regexp.MustCompile(`(?i)\bthen (run|execute) (this|these|following)\b.*\b(commands?|code)\b`),
			regexp.MustCompile(`(?i)\btool_call\b.*\b(bypass|ignore|skip)\b`),
			regexp.MustCompile(`(?i)\binject\b.*\b(sql|command|code|script)\b`),
		},
	}

	d.categories["data_exfiltration"] = injectionCategory{
		name:     "data_exfiltration",
		severity: InjectionHigh,
		weight:   2.0,
		enabled:  true,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b(print|output|show|display|reveal|leak)\b.*\b(apikeys?|secrets?|credentials?|passwords?|tokens?)\b`),
			regexp.MustCompile(`(?i)\b(steal|exfiltrate|exfiltration|dump)\b.*\b(database|data|information)\b`),
			regexp.MustCompile(`(?i)\bsend (all )?(data|information|content)\b.*\b(to|over)\b`),
			regexp.MustCompile(`(?i)\b(http://|https://)\S+\b.*\b(collect|gather|send|post)\b`),
		},
	}

	d.categories["prompt_leakage"] = injectionCategory{
		name:     "prompt_leakage",
		severity: InjectionMedium,
		weight:   1.5,
		enabled:  true,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b(what|show|reveal|dump)\b.*\b(system\s*prompt|instructions?|rules?|configuration)\b`),
			regexp.MustCompile(`(?i)\b(print|echo|display)\b.*\b(your|the|all)\b.*\b(prompt|system|instructions)\b`),
			regexp.MustCompile(`(?i)\brepeat (after|back)\b.*\b(your|the)\b.*\b(system|prompt|instructions)\b`),
			regexp.MustCompile(`(?i)\b(ignore|override|bypass)\b.*\b(safety|content.filter|alignment)\b`),
		},
	}

	d.categories["jailbreak_attempt"] = injectionCategory{
		name:     "jailbreak_attempt",
		severity: InjectionHigh,
		weight:   2.0,
		enabled:  true,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bDAN\b.*\b(MODE|prompt)\b`),
			regexp.MustCompile(`(?i)\b(grandma|old|retired)\b.*\b(you are a|act as a)\b`),
			regexp.MustCompile(`(?i)\bhypothetical(ly)?\b.*\b(scenario|situation|world)\b.*\b(no restrictions|no limits)\b`),
			regexp.MustCompile(`(?i)\bin (the|a)\b.*\b(matrix|simulation|game)\b`),
			regexp.MustCompile(`(?i)\b(no (more)? (rules?|restrictions?|limits?))\b`),
		},
	}

	d.categories["encoding_evasion"] = injectionCategory{
		name:     "encoding_evasion",
		severity: InjectionMedium,
		weight:   1.5,
		enabled:  true,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)base64\s*[:=]\s*[A-Za-z0-9+/=]{20,}`),
			regexp.MustCompile(`(?i)encode (this|these|following).*base64`),
			regexp.MustCompile(`(?i)\b(obfuscate|encode|decode)\b.*\b(payload|command|script)\b`),
			regexp.MustCompile(`(?i)\b\\x[0-9a-fA-F]{2}\b.*\b(payload|inject|attack)\b`),
		},
	}
}

// SetCategoryEnabled enables or disables a detection category at runtime.
func (d *PromptInjectionDetector) SetCategoryEnabled(name string, enabled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cat, ok := d.categories[name]; ok {
		cat.enabled = enabled
		d.categories[name] = cat
	}
}

// SetThreshold adjusts the auto-block score threshold.
func (d *PromptInjectionDetector) SetThreshold(score float64, blockLevel InjectionSeverity) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxScore = score
	d.autoBlock = blockLevel
}

// Check analyzes text for prompt injection patterns and returns a report.
func (d *PromptInjectionDetector) Check(text string) InjectionReport {
	return d.CheckWithSession("", text)
}

// CheckWithSession analyzes text and tracks per-session statistics.
func (d *PromptInjectionDetector) CheckWithSession(sessionID, text string) InjectionReport {
	d.mu.Lock()
	now := time.Now()
	if sessionID != "" {
		stats, ok := d.sessionStats[sessionID]
		if !ok {
			stats = &sessionInjectionStats{}
			d.sessionStats[sessionID] = stats
		}
		stats.totalChecks++
		stats.lastCheckAt = now
	}
	d.mu.Unlock()

	var matches []InjectionMatch
	totalWeight := 0.0
	maxSeverity := InjectionLow

	d.mu.RLock()
	for catName, cat := range d.categories {
		if !cat.enabled {
			continue
		}
		for _, re := range cat.patterns {
			locs := re.FindAllStringIndex(text, -1)
			for _, loc := range locs {
				matchText := text[loc[0]:loc[1]]
				contextStart := loc[0] - 30
				if contextStart < 0 {
					contextStart = 0
				}
				contextEnd := loc[1] + 30
				if contextEnd > len(text) {
					contextEnd = len(text)
				}
				matches = append(matches, InjectionMatch{
					Pattern:  catName + ":" + re.String(),
					Severity: cat.severity,
					Category: catName,
					Position: loc[0],
					Context:  strings.ReplaceAll(text[contextStart:contextEnd], "\n", " "),
				})
				totalWeight += cat.weight
				if cat.severity > maxSeverity {
					maxSeverity = cat.severity
				}
				_ = matchText
			}
		}
	}
	d.mu.RUnlock()

	score := 0.0
	if len(text) > 0 && totalWeight > 0 {
		score = totalWeight / float64(len(d.categories))
		if score > 1.0 {
			score = 1.0
		}
	}

	detected := len(matches) > 0

	if sessionID != "" && detected {
		d.mu.Lock()
		if stats, ok := d.sessionStats[sessionID]; ok {
			stats.totalDetections++
			switch maxSeverity {
			case InjectionCritical:
				stats.criticalCount++
			case InjectionHigh:
				stats.highCount++
			case InjectionMedium:
				stats.mediumCount++
			}
		}
		d.mu.Unlock()
	}

	return InjectionReport{
		Detected:  detected,
		Score:     score,
		Matches:   matches,
		InputLen:  len(text),
		CheckedAt: now,
	}
}

// ShouldBlock determines whether the report's severity warrants blocking.
func (r InjectionReport) ShouldBlock(autoBlockLevel InjectionSeverity) bool {
	if !r.Detected {
		return false
	}
	for _, m := range r.Matches {
		if m.Severity >= autoBlockLevel {
			return true
		}
	}
	return false
}

// GetSessionStats returns injection detection statistics for a session.
func (d *PromptInjectionDetector) GetSessionStats(sessionID string) (totalChecks, totalDetections, critical, high, medium int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	stats, ok := d.sessionStats[sessionID]
	if !ok {
		return 0, 0, 0, 0, 0
	}
	return stats.totalChecks, stats.totalDetections, stats.criticalCount, stats.highCount, stats.mediumCount
}

// ResetSessionStats clears detection statistics for a session.
func (d *PromptInjectionDetector) ResetSessionStats(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sessionStats, sessionID)
}

// GetTotalDetectionsForAdaptive returns the total detection count for adaptive
// circuit breaker integration. Returns 0 for unknown sessions.
func (d *PromptInjectionDetector) GetTotalDetectionsForAdaptive(sessionID string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	stats, ok := d.sessionStats[sessionID]
	if !ok {
		return 0
	}
	return stats.totalDetections
}

// CleanupOldSessions removes statistics for sessions not checked recently.
func (d *PromptInjectionDetector) CleanupOldSessions(maxAge time.Duration) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for id, stats := range d.sessionStats {
		if stats.lastCheckAt.Before(cutoff) {
			delete(d.sessionStats, id)
			removed++
		}
	}
	return removed
}
