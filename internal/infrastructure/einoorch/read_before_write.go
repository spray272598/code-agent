package einoorch

import (
	"context"
	"fmt"
	"sync"
)

// ReadBeforeWritePolicy enforces that a file must be read before it can be edited.
// This prevents accidental overwrites and ensures the agent has current file state.
type ReadBeforeWritePolicy struct {
	mu    sync.RWMutex
	reads map[string]bool // tracks which files have been read in this session
}

// NewReadBeforeWritePolicy creates a new policy instance.
func NewReadBeforeWritePolicy() *ReadBeforeWritePolicy {
	return &ReadBeforeWritePolicy{
		reads: make(map[string]bool),
	}
}

// RecordRead marks a file as having been read.
func (p *ReadBeforeWritePolicy) RecordRead(filePath string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads[filePath] = true
}

// HasRead returns true if the file has been read.
func (p *ReadBeforeWritePolicy) HasRead(filePath string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.reads[filePath]
}

// Reset clears all read records (for new sessions).
func (p *ReadBeforeWritePolicy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads = make(map[string]bool)
}

// EnforcementResult describes the outcome of a read-before-write check.
type EnforcementResult struct {
	Allowed bool
	Reason  string
}

// Enforce checks if a write/edit operation is allowed based on the read-before-write policy.
// writeTools are tool names that modify files (e.g., "write_file", "edit_file", "apply_patch").
// readTools are tool names that read files (e.g., "read_file").
func (p *ReadBeforeWritePolicy) Enforce(toolName string, args map[string]any, writeTools, readTools map[string]bool) *EnforcementResult {
	// Only enforce for write tools
	if !writeTools[toolName] {
		return &EnforcementResult{Allowed: true}
	}

	// Extract file path from args
	filePath := extractFilePath(toolName, args)
	if filePath == "" {
		// Can't determine file path, allow (fail open)
		return &EnforcementResult{Allowed: true}
	}

	// Check if file has been read
	if p.HasRead(filePath) {
		return &EnforcementResult{Allowed: true}
	}

	return &EnforcementResult{
		Allowed: false,
		Reason:  fmt.Sprintf("file %s must be read before editing (read-before-write policy)", filePath),
	}
}

// extractFilePath extracts the file path from tool arguments.
func extractFilePath(toolName string, args map[string]any) string {
	switch toolName {
	case "write_file":
		if path, ok := args["file_path"].(string); ok {
			return path
		}
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "edit_file":
		if path, ok := args["file_path"].(string); ok {
			return path
		}
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "apply_patch":
		if path, ok := args["file_path"].(string); ok {
			return path
		}
		if path, ok := args["path"].(string); ok {
			return path
		}
	}
	return ""
}

// DefaultWriteTools returns the set of tools that modify files.
func DefaultWriteTools() map[string]bool {
	return map[string]bool{
		"write_file":  true,
		"edit_file":   true,
		"apply_patch": true,
	}
}

// DefaultReadTools returns the set of tools that read files.
func DefaultReadTools() map[string]bool {
	return map[string]bool{
		"read_file": true,
		"glob":      true,
		"grep":      true,
	}
}

// ReadBeforeWriteGuard wraps a GuardedTool to enforce read-before-write policy.
type ReadBeforeWriteGuard struct {
	inner      *GuardedTool
	policy     *ReadBeforeWritePolicy
	writeTools map[string]bool
	readTools  map[string]bool
}

// NewReadBeforeWriteGuard creates a new guard wrapping a GuardedTool.
func NewReadBeforeWriteGuard(inner *GuardedTool, policy *ReadBeforeWritePolicy) *ReadBeforeWriteGuard {
	return &ReadBeforeWriteGuard{
		inner:      inner,
		policy:     policy,
		writeTools: DefaultWriteTools(),
		readTools:  DefaultReadTools(),
	}
}

// ExecCross enforces read-before-write before delegating to the inner tool.
func (g *ReadBeforeWriteGuard) ExecCross(ctx context.Context, name string, args map[string]any, sessionID, userID string, cross *CrossCut) (string, error) {
	// Record reads
	if g.readTools[name] {
		if filePath := extractFilePath(name, args); filePath != "" {
			g.policy.RecordRead(filePath)
		}
	}

	// Enforce write policy
	if result := g.policy.Enforce(name, args, g.writeTools, g.readTools); !result.Allowed {
		return "", fmt.Errorf("policy violation: %s", result.Reason)
	}

	return g.inner.execCross(ctx, name, args, sessionID, userID, cross)
}
