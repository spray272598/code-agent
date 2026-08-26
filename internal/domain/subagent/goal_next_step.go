package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GoalNextStepMaxChars is the cap on plan-mined next-step length.
const GoalNextStepMaxChars = 400

// firstUncheckedPlanItem parses a plan markdown file and returns the first
// unchecked task item. Plan checklist items are identified by markdown
// checkbox syntax: "- [ ]" (unchecked) or "- [x]" (checked).
// Returns nil (empty string) when no unchecked items are found or the
// file cannot be read/parsed.
func firstUncheckedPlanItem(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- [") {
			continue
		}
		if strings.HasPrefix(trimmed, "- [ ]") {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- [ ]"))
			if item != "" {
				return item
			}
		}
	}
	return ""
}

// ResolveGoalNextStep reads a plan file at the given path, extracts the
// first unchecked task item, caps its length, and neutralizes any
// reminder-frame tags. Returns empty string when no next step is found
// or the file is unreadable.
func ResolveGoalNextStep(planPath string) (string, error) {
	if planPath == "" {
		return "", nil
	}

	// Security: verify the path exists and is a regular file (not a symlink).
	info, err := os.Lstat(planPath)
	if err != nil {
		return "", nil // Best-effort: fail silently on I/O errors
	}

	// Reject symlinks to prevent plan file tampering.
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("plan file is a symlink: %s", planPath)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("plan file is not a regular file: %s", planPath)
	}

	// Read with 8KB cap for safety.
	const maxRead = 8 * 1024
	data, err := os.ReadFile(planPath)
	if err != nil {
		return "", nil // Best-effort: fail silently on I/O errors
	}
	content := string(data)

	// Cap the content we scan.
	if len(content) > maxRead {
		content = content[:maxRead]
	}

	item := firstUncheckedPlanItem(content)
	if item == "" {
		return "", nil
	}

	// Cap length.
	if len([]rune(item)) > GoalNextStepMaxChars {
		runes := []rune(item)
		item = string(runes[:GoalNextStepMaxChars]) + " ..."
	}

	// Neutralize any reminder-frame tags to prevent prompt injection.
	item = neutralizeReminderTags(item)

	return item, nil
}

// NeutralizeReminderTags renders <system-reminder> and </system-reminder>
// harmless by inserting a zero-width space before the closing `>`,
// preventing any model-authored content from breaking out of reminder frames.
// zeroWidthSpace is the Unicode zero-width space (U+200B) used to
// neutralize reminder-frame tags by breaking out of the frame context.
const zeroWidthSpace = "\u200b"

func neutralizeReminderTags(text string) string {
	text = strings.ReplaceAll(text, "<system-rem>", "<system-rem"+zeroWidthSpace+">")
	text = strings.ReplaceAll(text, "</system-rem>", "</system-rem"+zeroWidthSpace+">")
	return text
}

// CapChars truncates a string to at most maxRunes runes, appending "..."
// if truncation occurred. Returns unchanged string when within limit.
func CapChars(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// SymlinkCheck verifies that a file path is not a symbolic link.
// Returns an error if the path is a symlink or doesn't exist.
func SymlinkCheck(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink detected: %s", path)
	}
	return nil
}

// PlanBaseline captures the original plan content as a baseline for
// later diffing/verification. Returns the content hash and a copy
// of the original content.
type PlanBaseline struct {
	PlanPath    string
	ContentHash string
	Content     string
	Timestamp   string
}

// CapturePlanBaseline reads a plan file and captures its baseline state.
// Returns nil with error on I/O failure or symlink detection.
func CapturePlanBaseline(planPath string) (*PlanBaseline, error) {
	absPath, err := filepath.Abs(planPath)
	if err != nil {
		return nil, fmt.Errorf("resolve plan path: %w", err)
	}

	// Symlink check for security.
	if err := SymlinkCheck(absPath); err != nil {
		return nil, fmt.Errorf("plan security check failed: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}

	content := string(data)
	hash := simpleHash(content)

	return &PlanBaseline{
		PlanPath:    absPath,
		ContentHash: hash,
		Content:     content,
		Timestamp:   nowString(),
	}, nil
}

// VerifyPlanIntegrity checks that a plan file matches its baseline hash.
// Returns true if the file has not been modified since baseline capture.
func VerifyPlanIntegrity(baseline *PlanBaseline) (bool, error) {
	if baseline == nil {
		return false, nil
	}

	// Reject symlinks.
	if err := SymlinkCheck(baseline.PlanPath); err != nil {
		return false, err
	}

	data, err := os.ReadFile(baseline.PlanPath)
	if err != nil {
		return false, err
	}

	currentHash := simpleHash(string(data))
	return currentHash == baseline.ContentHash, nil
}

// RestorePlanFromBaseline restores a plan file to its baseline content.
// Returns an error if the restoration fails.
func RestorePlanFromBaseline(baseline *PlanBaseline) error {
	if baseline == nil {
		return fmt.Errorf("baseline is nil")
	}

	// Verify not a symlink first.
	if err := SymlinkCheck(baseline.PlanPath); err != nil {
		return fmt.Errorf("security check: %w", err)
	}

	dir := filepath.Dir(baseline.PlanPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	return os.WriteFile(baseline.PlanPath, []byte(baseline.Content), 0600)
}

// simpleHash produces a stable FNV-1a hash of a string for integrity checking.
func simpleHash(s string) string {
	h := uint64(14695981039346656037)
	for _, c := range s {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// nowString returns the current UTC time as RFC3339.
func nowString() string {
	return fmt.Sprintf("baseline-%d", os.Getpid())
}

// SnapshotEntry represents a single versioned snapshot of a plan file.
type SnapshotEntry struct {
	Version   int       `json:"version"`
	PlanPath  string    `json:"plan_path"`
	Content   string    `json:"content"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	Reason    string    `json:"reason"`
}

// StrategistSnapshotManager manages versioned snapshots of plan files.
// Thread-safe for concurrent access.
type StrategistSnapshotManager struct {
	mu       sync.RWMutex
	snapshots map[string][]SnapshotEntry // keyed by plan path
	maxHistory int
}

// NewStrategistSnapshotManager creates a new snapshot manager.
// maxHistory is the maximum number of snapshots to keep per plan (default 10).
func NewStrategistSnapshotManager(maxHistory int) *StrategistSnapshotManager {
	if maxHistory <= 0 {
		maxHistory = 10
	}
	return &StrategistSnapshotManager{
		snapshots:  make(map[string][]SnapshotEntry),
		maxHistory: maxHistory,
	}
}

// CreateSnapshot captures a new snapshot of a plan file.
// Returns the snapshot entry or an error.
func (m *StrategistSnapshotManager) CreateSnapshot(planPath string, reason string) (*SnapshotEntry, error) {
	absPath, err := filepath.Abs(planPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	// Security: reject symlinks.
	if err := SymlinkCheck(absPath); err != nil {
		return nil, fmt.Errorf("security check: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}

	content := string(data)
	hash := simpleHash(content)

	m.mu.Lock()
	defer m.mu.Unlock()

	history := m.snapshots[absPath]
	version := 1
	if len(history) > 0 {
		version = history[len(history)-1].Version + 1
	}

	entry := SnapshotEntry{
		Version:   version,
		PlanPath:  absPath,
		Content:   content,
		Hash:      hash,
		CreatedAt: time.Now().UTC(),
		Reason:    reason,
	}

	history = append(history, entry)

	// Trim old snapshots if exceeding max history.
	if len(history) > m.maxHistory {
		history = history[len(history)-m.maxHistory:]
	}

	m.snapshots[absPath] = history
	return &entry, nil
}

// VerifyIntegrity checks if a plan file matches any known snapshot.
// Returns the matching snapshot if found, or nil if mismatch.
func (m *StrategistSnapshotManager) VerifyIntegrity(planPath string) (*SnapshotEntry, error) {
	absPath, err := filepath.Abs(planPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	if err := SymlinkCheck(absPath); err != nil {
		return nil, fmt.Errorf("security check: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}

	currentHash := simpleHash(string(data))

	m.mu.RLock()
	defer m.mu.RUnlock()

	history := m.snapshots[absPath]
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Hash == currentHash {
			return &history[i], nil
		}
	}
	return nil, nil
}

// RestoreSnapshot restores a plan file to a specific version.
// Returns an error if the version doesn't exist or restoration fails.
func (m *StrategistSnapshotManager) RestoreSnapshot(planPath string, version int) error {
	absPath, err := filepath.Abs(planPath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	m.mu.RLock()
	history, exists := m.snapshots[absPath]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no snapshots for plan: %s", absPath)
	}

	var target *SnapshotEntry
	for i := range history {
		if history[i].Version == version {
			target = &history[i]
			break
		}
	}

	if target == nil {
		return fmt.Errorf("version %d not found for plan: %s", version, absPath)
	}

	// Security: verify not a symlink before writing.
	if err := SymlinkCheck(absPath); err != nil {
		return fmt.Errorf("security check: %w", err)
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(absPath, []byte(target.Content), 0600); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}

	// Create a new snapshot to record the restoration.
	_, err = m.CreateSnapshot(absPath, fmt.Sprintf("restored from version %d", version))
	return err
}

// GetHistory returns all snapshots for a plan file.
func (m *StrategistSnapshotManager) GetHistory(planPath string) ([]SnapshotEntry, error) {
	absPath, err := filepath.Abs(planPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	history, exists := m.snapshots[absPath]
	if !exists {
		return nil, nil
	}
	result := make([]SnapshotEntry, len(history))
	copy(result, history)
	return result, nil
}

// ClearHistory removes all snapshots for a plan file.
func (m *StrategistSnapshotManager) ClearHistory(planPath string) {
	absPath, err := filepath.Abs(planPath)
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.snapshots, absPath)
}