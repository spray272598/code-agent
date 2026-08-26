package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrEmptyPrompt          = errors.New("subagent prompt cannot be empty")
	ErrConflictingIsolation = errors.New("isolation=worktree and cwd are mutually exclusive")
	ErrTaskNotFound         = errors.New("subagent task not found")
	ErrTaskNotRunning       = errors.New("subagent task is not running")
	ErrTaskAlreadyExists    = errors.New("subagent task with this ID already exists")
	ErrMaxConcurrentReached = errors.New("max concurrent subagent limit reached")
	ErrTaskCancelled        = errors.New("subagent task was cancelled")
)

// SubagentRunner is the function signature for executing a subagent.
type SubagentRunner func(ctx context.Context, input SubagentInput) (SubagentOutput, error)

// SubagentCoordinator manages the lifecycle of subagent tasks.
type SubagentCoordinator struct {
	mu sync.RWMutex

	tasks          map[string]*subagentTask
	maxConcurrent  int
	runningCount   int
	runner         SubagentRunner
	progressCh     chan SubagentProgress
	onComplete     func(SubagentOutput)
	onError        func(error)
	defaultModel   string
}

type subagentTask struct {
	handle   SubagentHandle
	input    SubagentInput
	cancel   context.CancelFunc
	output   *SubagentOutput
	err      error
}

// NewSubagentCoordinator creates a new subagent coordinator.
func NewSubagentCoordinator(runner SubagentRunner, opts ...CoordinatorOption) *SubagentCoordinator {
	c := &SubagentCoordinator{
		tasks:         make(map[string]*subagentTask),
		maxConcurrent: 5,
		runner:        runner,
		progressCh:    make(chan SubagentProgress, 64),
		defaultModel:  "grok-3",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// CoordinatorOption configures the coordinator.
type CoordinatorOption func(*SubagentCoordinator)

// WithMaxConcurrent sets the maximum number of concurrent subagents.
func WithMaxConcurrent(n int) CoordinatorOption {
	return func(c *SubagentCoordinator) {
		if n > 0 {
			c.maxConcurrent = n
		}
	}
}

// WithDefaultModel sets the default model for subagents.
func WithDefaultModel(model string) CoordinatorOption {
	return func(c *SubagentCoordinator) {
		c.defaultModel = model
	}
}

// WithProgressChannel sets the channel for progress updates.
func WithProgressChannel(ch chan SubagentProgress) CoordinatorOption {
	return func(c *SubagentCoordinator) {
		if ch != nil {
			c.progressCh = ch
		}
	}
}

// WithOnComplete sets the callback for task completion.
func WithOnComplete(fn func(SubagentOutput)) CoordinatorOption {
	return func(c *SubagentCoordinator) {
		c.onComplete = fn
	}
}

// WithOnError sets the callback for task errors.
func WithOnError(fn func(error)) CoordinatorOption {
	return func(c *SubagentCoordinator) {
		c.onError = fn
	}
}

// Spawn creates and starts a new subagent task.
func (c *SubagentCoordinator) Spawn(ctx context.Context, input SubagentInput) (*SubagentHandle, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.runningCount >= c.maxConcurrent {
		c.mu.Unlock()
		return nil, ErrMaxConcurrentReached
	}

	taskID := input.TaskID
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", time.Now().UnixNano()%1e9)
	}
	if _, exists := c.tasks[taskID]; exists {
		c.mu.Unlock()
		return nil, ErrTaskAlreadyExists
	}

	ctx, cancel := context.WithCancel(ctx)
	handle := SubagentHandle{
		TaskID:       taskID,
		Status:       SubagentStatusPending,
		Description:  input.Description,
		SubagentType: input.SubagentType,
		StartedAt:    time.Now(),
		ParentSession: input.ParentSession,
	}

	task := &subagentTask{
		handle: handle,
		input:  input,
		cancel: cancel,
	}
	c.tasks[taskID] = task
	c.mu.Unlock()

	go c.runTask(ctx, task)

	return &handle, nil
}

// GetHandle returns the current handle for a task.
func (c *SubagentCoordinator) GetHandle(taskID string) (*SubagentHandle, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	task, ok := c.tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return &task.handle, nil
}

// Cancel cancels a running subagent task.
func (c *SubagentCoordinator) Cancel(taskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if task.handle.Status.IsTerminal() {
		return ErrTaskNotRunning
	}
	task.cancel()
	task.handle.Status = SubagentStatusCancelled
	return nil
}

// Wait blocks until a task completes and returns its output.
func (c *SubagentCoordinator) Wait(ctx context.Context, taskID string) (SubagentOutput, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return SubagentOutput{}, ctx.Err()
		case <-ticker.C:
			c.mu.RLock()
			task, ok := c.tasks[taskID]
			if !ok {
				c.mu.RUnlock()
				return SubagentOutput{}, ErrTaskNotFound
			}
			if task.output != nil {
				c.mu.RUnlock()
				return *task.output, nil
			}
			if task.err != nil {
				c.mu.RUnlock()
				return SubagentOutput{}, task.err
			}
			c.mu.RUnlock()
		}
	}
}

// List returns all task handles.
func (c *SubagentCoordinator) List() []SubagentHandle {
	c.mu.RLock()
	defer c.mu.RUnlock()
	handles := make([]SubagentHandle, 0, len(c.tasks))
	for _, task := range c.tasks {
		handles = append(handles, task.handle)
	}
	return handles
}

// RunningCount returns the number of currently running subagents.
func (c *SubagentCoordinator) RunningCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runningCount
}

// ProgressChan returns the channel for progress updates.
func (c *SubagentCoordinator) ProgressChan() <-chan SubagentProgress {
	return c.progressCh
}

// Cleanup removes completed tasks from the tracker.
func (c *SubagentCoordinator) Cleanup(maxAge time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	now := time.Now()
	for id, task := range c.tasks {
		if task.handle.Status.IsTerminal() && now.Sub(task.handle.StartedAt) > maxAge {
			delete(c.tasks, id)
			removed++
		}
	}
	return removed
}

func (c *SubagentCoordinator) runTask(ctx context.Context, task *subagentTask) {
	c.mu.Lock()
	task.handle.Status = SubagentStatusRunning
	c.runningCount++
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.runningCount--
		c.mu.Unlock()
	}()

	start := time.Now()
	input := task.input
	if input.Model == "" {
		input.Model = c.defaultModel
	}

	output, err := c.runner(ctx, input)
	output.DurationMs = time.Since(start).Milliseconds()
	output.CompletedAt = time.Now()

	c.mu.Lock()
	if errors.Is(err, context.Canceled) {
		output.Status = SubagentStatusCancelled
		output.Error = ErrTaskCancelled.Error()
		task.handle.Status = SubagentStatusCancelled
	} else if err != nil {
		output.Status = SubagentStatusFailed
		output.Error = err.Error()
		task.handle.Status = SubagentStatusFailed
		task.err = err
	} else {
		output.Status = SubagentStatusCompleted
		task.handle.Status = SubagentStatusCompleted
	}
	task.output = &output
	c.mu.Unlock()

	select {
	case c.progressCh <- SubagentProgress{
		TaskID:        output.TaskID,
		TokensUsed:    output.TokensUsed,
		FinishedTokens: output.TokensUsed,
		Message:       fmt.Sprintf("task %s completed: %s", task.handle.TaskID, output.Status),
		Timestamp:     time.Now(),
	}:
	default:
	}

	if output.Status == SubagentStatusCompleted && c.onComplete != nil {
		c.onComplete(output)
	}
	if output.Status == SubagentStatusFailed && c.onError != nil {
		c.onError(fmt.Errorf("task %s failed: %s", task.handle.TaskID, output.Error))
	}
}

// ParentContext holds the parent agent's context for subagent inheritance.
type ParentContext struct {
	SessionID      string                 `json:"session_id"`
	ToolDefinitions []ToolDefinition      `json:"tool_definitions,omitempty"`
	MCPServers     []MCPServerInfo        `json:"mcp_servers,omitempty"`
	Hooks          map[string]string      `json:"hooks,omitempty"`
	Metadata       map[string]string      `json:"metadata,omitempty"`
}

// ToolDefinition describes a tool available to a subagent.
type ToolDefinition struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Schema      string   `json:"schema,omitempty"`
	Required    []string `json:"required,omitempty"`
}

// MCPServerInfo describes an MCP server available to a subagent.
type MCPServerInfo struct {
	Name     string   `json:"name"`
	Endpoint string   `json:"endpoint"`
	Tools    []string `json:"tools,omitempty"`
}

// BuildSpawnContext constructs a SubagentInput with parent context.
func BuildSpawnContext(parent ParentContext, input SubagentInput) SubagentInput {
	if input.ParentSession == "" {
		input.ParentSession = parent.SessionID
	}
	if input.Metadata == nil {
		input.Metadata = make(map[string]string)
	}
	for k, v := range parent.Metadata {
		if _, exists := input.Metadata[k]; !exists {
			input.Metadata[k] = v
		}
	}
	return input
}

// DefaultRunner returns a simple runner that can be used for testing.
func DefaultRunner() SubagentRunner {
	return func(ctx context.Context, input SubagentInput) (SubagentOutput, error) {
		if ctx.Err() != nil {
			return SubagentOutput{}, ctx.Err()
		}
		progress := SubagentProgress{
			TaskID:    input.TaskID,
			Step:      1,
			Message:   fmt.Sprintf("Processing: %s", input.Description),
			Timestamp: time.Now(),
		}
		_ = progress
		return SubagentOutput{
			TaskID:     input.TaskID,
			Status:     SubagentStatusCompleted,
			Result:     fmt.Sprintf("Completed task: %s", input.Description),
			TokensUsed: 100,
			Model:      input.Model,
		}, nil
	}
}
