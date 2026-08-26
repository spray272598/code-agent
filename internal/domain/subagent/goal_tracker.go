package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// GoalPhase identifies the lifecycle phase of a goal.
type GoalPhase int

const (
	GoalPhaseIdle GoalPhase = iota
	GoalPhasePlanning
	GoalPhaseExecuting
)

func (p GoalPhase) String() string {
	switch p {
	case GoalPhasePlanning:
		return "planning"
	case GoalPhaseExecuting:
		return "executing"
	default:
		return "idle"
	}
}

// GoalStatus identifies the lifecycle state of a goal.
type GoalStatus int

const (
	GoalStatusActive GoalStatus = iota
	GoalStatusUserPaused
	GoalStatusBackOffPaused
	GoalStatusNoProgressPaused
	GoalStatusInfraPaused
	GoalStatusBlocked
	GoalStatusBudgetLimited
	GoalStatusComplete
)

func (s GoalStatus) String() string {
	switch s {
	case GoalStatusUserPaused:
		return "user_paused"
	case GoalStatusBackOffPaused:
		return "back_off_paused"
	case GoalStatusNoProgressPaused:
		return "no_progress_paused"
	case GoalStatusInfraPaused:
		return "infra_paused"
	case GoalStatusBlocked:
		return "blocked"
	case GoalStatusBudgetLimited:
		return "budget_limited"
	case GoalStatusComplete:
		return "complete"
	default:
		return "active"
	}
}

// IsPaused returns true for any paused variant.
func (s GoalStatus) IsPaused() bool {
	switch s {
	case GoalStatusUserPaused, GoalStatusBackOffPaused,
		GoalStatusNoProgressPaused, GoalStatusInfraPaused, GoalStatusBlocked:
		return true
	default:
		return false
	}
}

// GoalPauseReason identifies why a goal was paused.
type GoalPauseReason int

const (
	GoalPauseUser GoalPauseReason = iota
	GoalPauseBackOff
	GoalPauseNoProgress
	GoalPauseVerification
	GoalPauseInfra
)

// GoalEvent is a lifecycle event recorded in goal history.
type GoalEvent int

const (
	GoalEventCreated GoalEvent = iota
	GoalEventPlanningStarted
	GoalEventPlanningCompleted
	GoalEventPlanningFailed
	GoalEventWorkerStarted
	GoalEventWorkerCompleted
	GoalEventWorkerFailed
	GoalEventContextRotated
	GoalEventGoalPaused
	GoalEventGoalResumed
	GoalEventGoalCompleted
	GoalEventGoalCleared
	GoalEventBudgetExceeded
	GoalEventPrematureStopDetected
	GoalEventStallDetected
	GoalEventStrategistFired
	GoalEventClassifierFired
)

func (e GoalEvent) String() string {
	names := map[GoalEvent]string{
		GoalEventCreated: "goal_created",
		GoalEventPlanningStarted: "planning_started",
		GoalEventPlanningCompleted: "planning_completed",
		GoalEventPlanningFailed: "planning_failed",
		GoalEventWorkerStarted: "worker_started",
		GoalEventWorkerCompleted: "worker_completed",
		GoalEventWorkerFailed: "worker_failed",
		GoalEventContextRotated: "context_rotated",
		GoalEventGoalPaused: "goal_paused",
		GoalEventGoalResumed: "goal_resumed",
		GoalEventGoalCompleted: "goal_completed",
		GoalEventGoalCleared: "goal_cleared",
		GoalEventBudgetExceeded: "budget_exceeded",
		GoalEventPrematureStopDetected: "premature_stop_detected",
		GoalEventStallDetected: "stall_detected",
		GoalEventStrategistFired: "strategist_fired",
		GoalEventClassifierFired: "classifier_fired",
	}
	if n, ok := names[e]; ok {
		return n
	}
	return "unknown"
}

// HistoryEntry records a single goal lifecycle event.
type HistoryEntry struct {
	Event     GoalEvent
	Timestamp time.Time
	Message   string
}

// GoalSnapshot is a serializable view of the current goal state.
type GoalSnapshot struct {
	ID                    string     `json:"id"`
	Objective             string     `json:"objective"`
	Status                GoalStatus `json:"status"`
	Phase                 GoalPhase  `json:"phase"`
	TokenBudget           int64      `json:"tokenBudget"`
	TokensUsed            int64      `json:"tokensUsed"`
	ElapsedMs             int64      `json:"elapsedMs"`
	CurrentSubagentRole   string     `json:"currentSubagentRole,omitempty"`
	TotalWorkerRounds     int        `json:"totalWorkerRounds"`
	TotalVerifyRounds     int        `json:"totalVerifyRounds"`
	TokenBaseline         int64      `json:"tokenBaseline"`
	FinishedSubagentTokens int64     `json:"finishedSubagentTokens"`
	LiveSubagentTokens    int64      `json:"liveSubagentTokens,omitempty"`
	LastEvent             string     `json:"lastEvent,omitempty"`
	ConsecutiveFailures   int        `json:"consecutiveFailures"`
	StallGapFingerprint   string     `json:"stallGapFingerprint,omitempty"`
	PauseMessage          string     `json:"pauseMessage,omitempty"`
	BlockerKey            string     `json:"blockerKey,omitempty"`
	BlockerCount          int        `json:"blockerCount,omitempty"`
	ScratchRoot           string     `json:"scratchRoot,omitempty"`
	VerifierID            string     `json:"verifierID,omitempty"`
	VerifierCount         int        `json:"verifierCount,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

// GoalTracker is a pure state machine (no I/O) that manages goal lifecycle.
type GoalTracker struct {
	mu sync.Mutex

	id        string
	objective string
	phase     GoalPhase
	status    GoalStatus

	tokenBudget int64
	tokensUsed  int64
	tokenBaseline int64

	createdAt time.Time
	updatedAt time.Time

	currentRole   string
	totalWorkers  int
	totalVerifies int

	consecutiveFailures int
	stallFingerprint    string
	stallCount          int

	strategistCount int

	pauseMessage string
	blockerKey   string
	blockerCount int
	completed    bool

	// scratchRoot is the temporary workspace for sub-agent operations
	scratchRoot string
	// verifierID identifies the current verifier for multi-panel verification
	verifierID string
	// verifierCount tracks total verification rounds
	verifierCount int

	history []HistoryEntry
	maxHist int
}

// NewGoalTracker creates a new goal tracker.
func NewGoalTracker(id, objective string, tokenBudget int64) *GoalTracker {
	now := time.Now()
	return &GoalTracker{
		id:           id,
		objective:    objective,
		phase:        GoalPhaseIdle,
		status:       GoalStatusActive,
		tokenBudget:  tokenBudget,
		tokensUsed:   0,
		tokenBaseline: 0,
		createdAt:    now,
		updatedAt:    now,
		maxHist:      64,
	}
}

// ID returns the goal's unique identifier.
func (g *GoalTracker) ID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.id
}

// Status returns the current goal status.
func (g *GoalTracker) Status() GoalStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status
}

// Phase returns the current goal phase.
func (g *GoalTracker) Phase() GoalPhase {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.phase
}

// TokensUsed returns total tokens consumed so far.
func (g *GoalTracker) TokensUsed() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokensUsed
}

// Elapsed returns elapsed time in milliseconds since goal creation.
func (g *GoalTracker) Elapsed() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Since(g.createdAt).Milliseconds()
}

// Snapshot returns a serializable view of current state.
func (g *GoalTracker) Snapshot() GoalSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	var lastEvent string
	if len(g.history) > 0 {
		lastEvent = g.history[len(g.history)-1].Event.String()
	}
	return GoalSnapshot{
		ID:                  g.id,
		Objective:           g.objective,
		Status:              g.status,
		Phase:               g.phase,
		TokenBudget:         g.tokenBudget,
		TokensUsed:          g.tokensUsed,
		ElapsedMs:           time.Since(g.createdAt).Milliseconds(),
		CurrentSubagentRole: g.currentRole,
		TotalWorkerRounds:   g.totalWorkers,
		TotalVerifyRounds:   g.totalVerifies,
		TokenBaseline:       g.tokenBaseline,
		FinishedSubagentTokens: g.tokensUsed,
		LastEvent:           lastEvent,
		ConsecutiveFailures: g.consecutiveFailures,
		StallGapFingerprint: g.stallFingerprint,
		PauseMessage:        g.pauseMessage,
		CreatedAt:           g.createdAt,
		UpdatedAt:           g.updatedAt,
	}
}

// RecordTokens adds token usage and returns true if budget exceeded.
func (g *GoalTracker) RecordTokens(n int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tokensUsed += n
	g.updatedAt = time.Now()
	if g.tokensUsed > g.tokenBudget {
		g.status = GoalStatusBudgetLimited
		g.recordLocked(GoalEventBudgetExceeded, "")
		return true
	}
	return false
}

// ResetBaseline sets the baseline token count for delta computation.
func (g *GoalTracker) ResetBaseline() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tokenBaseline = g.tokensUsed
}

// DeltaTokens returns tokens used since last baseline.
func (g *GoalTracker) DeltaTokens() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokensUsed - g.tokenBaseline
}

// Transitions ---------------------------------------------------------------

func (g *GoalTracker) StartPlanning() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.phase = GoalPhasePlanning
	g.status = GoalStatusActive
	g.recordLocked(GoalEventPlanningStarted, "")
	g.updatedAt = time.Now()
}

func (g *GoalTracker) CompletePlanning() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.phase = GoalPhaseExecuting
	g.recordLocked(GoalEventPlanningCompleted, "")
	g.updatedAt = time.Now()
}

func (g *GoalTracker) FailPlanning(msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = GoalStatusBlocked
	g.phase = GoalPhaseIdle
	g.pauseMessage = msg
	g.recordLocked(GoalEventPlanningFailed, msg)
	g.updatedAt = time.Now()
}

func (g *GoalTracker) StartWorker(role string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.currentRole = role
	g.totalWorkers++
	g.recordLocked(GoalEventWorkerStarted, role)
	g.updatedAt = time.Now()
}

func (g *GoalTracker) CompleteWorker(msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.currentRole = ""
	g.consecutiveFailures = 0
	g.recordLocked(GoalEventWorkerCompleted, msg)
	g.updatedAt = time.Now()
}

func (g *GoalTracker) FailWorker(msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.currentRole = ""
	g.consecutiveFailures++
	g.recordLocked(GoalEventWorkerFailed, msg)
	g.updatedAt = time.Now()
}

func (g *GoalTracker) RecordVerify() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.totalVerifies++
	g.recordLocked(GoalEventClassifierFired, "")
	g.updatedAt = time.Now()
}

func (g *GoalTracker) RecordStall(fingerprint string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stallFingerprint == fingerprint && fingerprint != "" {
		g.stallCount++
		if g.stallCount >= 2 {
			g.status = GoalStatusNoProgressPaused
			g.pauseMessage = fmt.Sprintf("stall detected: same gap fingerprint after %d attempts", g.stallCount)
			g.recordLocked(GoalEventStallDetected, fingerprint)
			return true
		}
	} else {
		g.stallFingerprint = fingerprint
		g.stallCount = 1
	}
	g.recordLocked(GoalEventClassifierFired, "")
	g.updatedAt = time.Now()
	return false
}

// GapFingerprint builds a stable hash from verifier gaps for stall detection.
func GapFingerprint(gaps []string) string {
	if len(gaps) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(strings.Join(gaps, "\x1f")))
	return hex.EncodeToString(h[:8])
}

// StrategistFired records a strategist invocation. Returns true if cap not
// exceeded (strategist_count < 3 by default).
func (g *GoalTracker) StrategistFired(max int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.strategistCount >= max {
		return false
	}
	g.strategistCount++
	g.recordLocked(GoalEventStrategistFired, "")
	g.updatedAt = time.Now()
	return true
}

// StallThreshold returns the number of consecutive identical fingerprints
// needed to trigger auto-pause (2).
func StallThreshold() int { return 2 }

// StrategistCapBonus returns the extra classifier rounds granted after a
// strategist fires (3).
func StrategistCapBonus() int { return 3 }

// PrematureStop records a premature-stop detection event.
func (g *GoalTracker) PrematureStop(pattern string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.recordLocked(GoalEventPrematureStopDetected, pattern)
	g.updatedAt = time.Now()
}

// Pause pauses the goal with the given reason.
func (g *GoalTracker) Pause(reason GoalPauseReason, msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch reason {
	case GoalPauseUser:
		g.status = GoalStatusUserPaused
	case GoalPauseBackOff:
		g.status = GoalStatusBackOffPaused
	case GoalPauseNoProgress:
		g.status = GoalStatusNoProgressPaused
	case GoalPauseVerification:
		g.status = GoalStatusBlocked
	case GoalPauseInfra:
		g.status = GoalStatusInfraPaused
	}
	g.pauseMessage = msg
	g.recordLocked(GoalEventGoalPaused, msg)
	g.updatedAt = time.Now()
}

// Resume resumes a paused goal.
func (g *GoalTracker) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = GoalStatusActive
	g.pauseMessage = ""
	g.recordLocked(GoalEventGoalResumed, "")
	g.updatedAt = time.Now()
}

// Complete marks the goal as complete.
func (g *GoalTracker) Complete() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = GoalStatusComplete
	g.completed = true
	g.recordLocked(GoalEventGoalCompleted, "")
	g.updatedAt = time.Now()
}

// IsComplete returns true if goal reached terminal state.
func (g *GoalTracker) IsComplete() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.completed
}

// SetBlocker records a stable blocker key for the goal.
func (g *GoalTracker) SetBlocker(key, msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockerKey = key
	g.blockerCount++
	g.pauseMessage = msg
	g.status = GoalStatusBlocked
	g.recordLocked(GoalEventGoalPaused, fmt.Sprintf("blocked:%s", key))
	g.updatedAt = time.Now()
}

// BlockerKey returns the current blocker key.
func (g *GoalTracker) BlockerKey() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blockerKey
}

// BlockerCount returns how many times the blocker has been set.
func (g *GoalTracker) BlockerCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blockerCount
}

// ScratchRoot returns the temporary workspace path.
func (g *GoalTracker) ScratchRoot() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.scratchRoot
}

// SetScratchRoot sets the temporary workspace path.
func (g *GoalTracker) SetScratchRoot(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scratchRoot = path
	g.recordLocked(GoalEventCreated, "scratch_root_set:"+path)
	g.updatedAt = time.Now()
}

// VerifierID returns the current verifier identifier.
func (g *GoalTracker) VerifierID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.verifierID
}

// SetVerifierID sets the verifier identifier for multi-panel verification.
func (g *GoalTracker) SetVerifierID(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.verifierID = id
	g.verifierCount++
	g.recordLocked(GoalEventClassifierFired, "verifier:"+id)
	g.updatedAt = time.Now()
}

// VerifierCount returns the total number of verification rounds.
func (g *GoalTracker) VerifierCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.verifierCount
}

// History returns a copy of the event history.
func (g *GoalTracker) History() []HistoryEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]HistoryEntry, len(g.history))
	copy(out, g.history)
	return out
}

// ConsecutiveFailures returns the current consecutive failure count.
func (g *GoalTracker) ConsecutiveFailures() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.consecutiveFailures
}

// Reset clears history and returns to initial Active state.
func (g *GoalTracker) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.phase = GoalPhaseIdle
	g.status = GoalStatusActive
	g.currentRole = ""
	g.totalWorkers = 0
	g.totalVerifies = 0
	g.consecutiveFailures = 0
	g.stallFingerprint = ""
	g.stallCount = 0
	g.strategistCount = 0
	g.pauseMessage = ""
	g.blockerKey = ""
	g.completed = false
	g.history = nil
	g.updatedAt = time.Now()
}

// recordLocked appends a history entry (caller must hold mu).
func (g *GoalTracker) recordLocked(ev GoalEvent, msg string) {
	if len(g.history) >= g.maxHist {
		g.history = g.history[1:]
	}
	g.history = append(g.history, HistoryEntry{
		Event:     ev,
		Timestamp: time.Now(),
		Message:   msg,
	})
}
