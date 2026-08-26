package orchestration

import (
	"fmt"
	"sync"
	"time"
)

// Status of an orchestration run.
type Status string

const (
	StatusInit        Status = "init"
	StatusRunning     Status = "running"
	StatusPaused      Status = "paused"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
)

// EntryType is the kind of journal entry.
type EntryType string

const (
	EntryStartRun EntryType = "start_run"
	EntryPhase    EntryType = "phase"
	EntryToolCall EntryType = "tool_call"
	EntryToolRes  EntryType = "tool_result"
	EntryTokenUse EntryType = "token_use"
	EntryPause    EntryType = "pause"
	EntryResume   EntryType = "resume"
	EntryComplete EntryType = "complete"
	EntryFail     EntryType = "fail"
	EntryCancel   EntryType = "cancel"
)

// JournalEntry is one line in the journal (JSONL).
type JournalEntry struct {
	Timestamp  time.Time `json:"ts"`
	Type       EntryType `json:"type"`
	RunID      string    `json:"runId"`
	PhaseID    string    `json:"phaseId,omitempty"`
	ParentID   string    `json:"parentId,omitempty"`
	Content    string    `json:"content,omitempty"`
	TokenDelta int       `json:"tokenDelta,omitempty"`
	Agent      string    `json:"agent,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// JournalState is the persisted in-memory snapshot of a run.
type JournalState struct {
	RunID       string            `json:"runId"`
	ParentID    string            `json:"parentId,omitempty"`
	Status      Status            `json:"status"`
	Goal        string            `json:"goal,omitempty"`
	AgentBudget int               `json:"agentBudget"`
	AgentsUsed  int               `json:"agentsUsed"`
	TokensUsed  int               `json:"tokensUsed"`
	PhasesDone  []string          `json:"phasesDone"`
	Results     map[string]string `json:"results,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// Journal persists orchestration progress for resumable execution (P1-1).
// Uses a pluggable JournalStorage backend (file, MySQL, Redis).
type Journal struct {
	mu      sync.Mutex
	storage JournalStorage
}

// NewJournal creates a Journal with file-based storage at dir/runID/journal.jsonl.
func NewJournal(dir, runID string) (*Journal, error) {
	s, err := NewFileJournalStorage(dir, runID)
	if err != nil {
		return nil, err
	}
	return &Journal{storage: s}, nil
}

// NewJournalWithStorage creates a Journal with a custom storage backend.
func NewJournalWithStorage(storage JournalStorage) *Journal {
	return &Journal{storage: storage}
}

// NewEphemeralJournal creates an in-memory journal (no persistence).
func NewEphemeralJournal() *Journal {
	return &Journal{storage: NewMemoryJournalStorage()}
}

// Append writes a journal entry atomically via the storage backend.
func (j *Journal) Append(e JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.storage == nil {
		return nil
	}
	return j.storage.Append(e)
}

// Close releases the storage backend resources.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.storage == nil {
		return nil
	}
	return j.storage.Close()
}

// Path returns the journal file path for file-based storage, empty otherwise.
func (j *Journal) Path() string {
	if s, ok := j.storage.(*FileJournalStorage); ok {
		return s.Path()
	}
	return ""
}

// Replay rebuilds a JournalState by replaying all entries from storage.
func (j *Journal) Replay(runID string) *JournalState {
	state := &JournalState{
		RunID:       runID,
		Status:      StatusInit,
		Results:     map[string]string{},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		AgentBudget: 4,
	}
	if j.storage == nil {
		return state
	}
	entries, err := j.storage.ReadAll(runID)
	if err != nil || len(entries) == 0 {
		return state
	}
	for _, e := range entries {
		switch e.Type {
		case EntryStartRun:
			state.Status = StatusRunning
			state.AgentsUsed = 0
			state.TokensUsed = 0
			state.PhasesDone = nil
			state.Results = map[string]string{}
			state.Goal = e.Content
			state.AgentBudget = 4
			state.UpdatedAt = e.Timestamp
		case EntryPhase:
			if e.PhaseID != "" {
				state.PhasesDone = appendUnique(state.PhasesDone, e.PhaseID)
				if e.Content != "" {
					state.Results[e.PhaseID] = e.Content
				}
			}
			state.AgentsUsed++
			state.UpdatedAt = e.Timestamp
		case EntryTokenUse:
			state.TokensUsed += e.TokenDelta
			state.UpdatedAt = e.Timestamp
		case EntryComplete:
			state.Status = StatusCompleted
			state.UpdatedAt = e.Timestamp
		case EntryPause:
			state.Status = StatusPaused
			state.UpdatedAt = e.Timestamp
		case EntryResume:
			state.Status = StatusRunning
			state.UpdatedAt = e.Timestamp
		case EntryFail:
			state.Status = StatusFailed
			state.UpdatedAt = e.Timestamp
		case EntryCancel:
			state.Status = StatusCancelled
			state.UpdatedAt = e.Timestamp
		}
	}
	return state
}

// IsResumable reports whether the given status can be resumed.
func IsResumable(s Status) bool {
	switch s {
	case StatusPaused, StatusInterrupted, StatusFailed:
		return true
	}
	return false
}

// StatusFromString parses a string into a Status.
func StatusFromString(s string) Status {
	switch Status(s) {
	case StatusInit, StatusRunning, StatusPaused, StatusCompleted,
		StatusFailed, StatusCancelled, StatusInterrupted:
		return Status(s)
	}
	return StatusInit
}

// Append helpers ---------------------------------------------------------

// LogStartRun writes a start entry.
func (j *Journal) LogStartRun(runID, goal string, agentBudget int) error {
	return j.Append(JournalEntry{
		RunID: runID, Type: EntryStartRun, Content: goal,
	})
}

// LogPhaseCompletion records a completed phase.
func (j *Journal) LogPhaseCompletion(runID, phaseID, output string) error {
	return j.Append(JournalEntry{
		RunID: runID, PhaseID: phaseID, Type: EntryPhase, Content: output,
	})
}

// LogTokenUse records token consumption.
func (j *Journal) LogTokenUse(runID string, tokens int) error {
	if tokens <= 0 {
		return nil
	}
	return j.Append(JournalEntry{
		RunID: runID, Type: EntryTokenUse, TokenDelta: tokens,
	})
}

// LogToolCall records a tool call entry.
func (j *Journal) LogToolCall(runID, phaseID, tool, args string) error {
	return j.Append(JournalEntry{
		RunID: runID, PhaseID: phaseID, Type: EntryToolCall,
		Agent: tool, Content: args,
	})
}

// LogToolResult records a tool result.
func (j *Journal) LogToolResult(runID, phaseID, tool, result string) error {
	return j.Append(JournalEntry{
		RunID: runID, PhaseID: phaseID, Type: EntryToolRes,
		Agent: tool, Content: result,
	})
}

// LogPause records a pause event.
func (j *Journal) LogPause(runID, reason string) error {
	return j.Append(JournalEntry{
		RunID: runID, Type: EntryPause, Content: reason,
	})
}

// LogResume records a resume event.
func (j *Journal) LogResume(runID, fromStatus string) error {
	return j.Append(JournalEntry{
		RunID: runID, Type: EntryResume, Content: fromStatus,
	})
}

// LogComplete marks the run as completed.
func (j *Journal) LogComplete(runID, summary string) error {
	return j.Append(JournalEntry{
		RunID: runID, Type: EntryComplete, Content: summary,
	})
}

// LogFail records a failure.
func (j *Journal) LogFail(runID, phaseID, err string) error {
	return j.Append(JournalEntry{
		RunID: runID, PhaseID: phaseID, Type: EntryFail, Error: err,
	})
}

// LogCancel records a cancellation.
func (j *Journal) LogCancel(runID, reason string) error {
	return j.Append(JournalEntry{
		RunID: runID, Type: EntryCancel, Content: reason,
	})
}

// FormatEntry renders an entry for human reading.
func FormatEntry(e JournalEntry) string {
	switch e.Type {
	case EntryStartRun:
		return fmt.Sprintf("[%s] START run=%s goal=%s", e.Timestamp.Format(time.Kitchen), e.RunID, truncateStr(e.Content, 40))
	case EntryPhase:
		return fmt.Sprintf("[%s] PHASE %s run=%s", e.Timestamp.Format(time.Kitchen), e.PhaseID, e.RunID)
	case EntryToolCall:
		return fmt.Sprintf("[%s] CALL %s run=%s phase=%s", e.Timestamp.Format(time.Kitchen), e.Agent, e.RunID, e.PhaseID)
	case EntryToolRes:
		return fmt.Sprintf("[%s] RES  %s run=%s phase=%s", e.Timestamp.Format(time.Kitchen), e.Agent, e.RunID, e.PhaseID)
	case EntryTokenUse:
		return fmt.Sprintf("[%s] TOKEN run=%s delta=%d", e.Timestamp.Format(time.Kitchen), e.RunID, e.TokenDelta)
	case EntryPause:
		return fmt.Sprintf("[%s] PAUSE run=%s reason=%s", e.Timestamp.Format(time.Kitchen), e.RunID, e.Content)
	case EntryResume:
		return fmt.Sprintf("[%s] RESUME run=%s from=%s", e.Timestamp.Format(time.Kitchen), e.RunID, e.Content)
	case EntryComplete:
		return fmt.Sprintf("[%s] COMPLETE run=%s", e.Timestamp.Format(time.Kitchen), e.RunID)
	case EntryFail:
		return fmt.Sprintf("[%s] FAIL run=%s phase=%s err=%s", e.Timestamp.Format(time.Kitchen), e.RunID, e.PhaseID, e.Error)
	default:
		return fmt.Sprintf("[%s] %s run=%s", e.Timestamp.Format(time.Kitchen), e.Type, e.RunID)
	}
}

// splitLines splits a byte slice into lines.
func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
