package tool

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type BackgroundExecutor struct {
	mu     sync.RWMutex
	tasks  map[string]*BackgroundTask
	toolFn map[string]ITool
	nextID int64
}

func NewBackgroundExecutor() *BackgroundExecutor {
	return &BackgroundExecutor{
		tasks:  make(map[string]*BackgroundTask),
		toolFn: make(map[string]ITool),
	}
}

func (be *BackgroundExecutor) RegisterTool(t ITool) {
	be.mu.Lock()
	defer be.mu.Unlock()
	be.toolFn[t.Name()] = t
}

func (be *BackgroundExecutor) Start(ctx context.Context, toolName string, args map[string]any) (string, error) {
	be.mu.Lock()
	be.nextID++
	id := fmt.Sprintf("bg-%d-%d", time.Now().UnixNano(), be.nextID)
	task := &BackgroundTask{
		ID:        id,
		ToolName:  toolName,
		Status:    TaskPending,
		Args:      args,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	be.tasks[id] = task
	be.mu.Unlock()

	go be.runTask(ctx, id, toolName, args)
	return id, nil
}

func (be *BackgroundExecutor) runTask(ctx context.Context, id, toolName string, args map[string]any) {
	be.mu.Lock()
	task := be.tasks[id]
	task.Status = TaskRunning
	task.UpdatedAt = time.Now()
	be.mu.Unlock()

	tool, ok := be.toolFn[toolName]
	if !ok {
		be.mu.Lock()
		task.Status = TaskFailed
		task.Error = fmt.Errorf("tool %s not registered", toolName)
		task.UpdatedAt = time.Now()
		be.mu.Unlock()
		return
	}

	result, err := tool.Execute(ctx, args)

	be.mu.Lock()
	defer be.mu.Unlock()
	task.UpdatedAt = time.Now()
	if err != nil {
		task.Status = TaskFailed
		task.Error = err
	} else if result.IsError {
		task.Status = TaskFailed
		task.Error = fmt.Errorf("tool error: %s", result.Text)
	} else {
		task.Status = TaskCompleted
		task.Result = result
	}
}

func (be *BackgroundExecutor) Get(id string) (*BackgroundTask, error) {
	be.mu.RLock()
	defer be.mu.RUnlock()
	task, ok := be.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	cp := *task
	return &cp, nil
}

func (be *BackgroundExecutor) WaitFor(id string, timeout time.Duration) (*BackgroundTask, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timeout waiting for task %s", id)
		case <-ticker.C:
			task, err := be.Get(id)
			if err != nil {
				return nil, err
			}
			if task.Status == TaskCompleted || task.Status == TaskFailed || task.Status == TaskCancelled {
				return task, nil
			}
		}
	}
}

func (be *BackgroundExecutor) Cancel(id string) error {
	be.mu.Lock()
	defer be.mu.Unlock()
	task, ok := be.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if task.Status != TaskPending && task.Status != TaskRunning {
		return ErrTaskNotCancellable
	}
	task.Status = TaskCancelled
	task.UpdatedAt = time.Now()
	return nil
}

func (be *BackgroundExecutor) List() []*BackgroundTask {
	be.mu.RLock()
	defer be.mu.RUnlock()
	var result []*BackgroundTask
	for _, task := range be.tasks {
		cp := *task
		result = append(result, &cp)
	}
	return result
}

func (be *BackgroundExecutor) Cleanup(maxAge time.Duration) int {
	be.mu.Lock()
	defer be.mu.Unlock()
	now := time.Now()
	cleaned := 0
	for id, task := range be.tasks {
		if task.Status == TaskCompleted || task.Status == TaskFailed || task.Status == TaskCancelled {
			if now.Sub(task.UpdatedAt) > maxAge {
				delete(be.tasks, id)
				cleaned++
			}
		}
	}
	return cleaned
}

type CompositionExecutor struct {
	be   *BackgroundExecutor
	tasks map[int]string
}

func NewCompositionExecutor(be *BackgroundExecutor) *CompositionExecutor {
	return &CompositionExecutor{
		be:    be,
		tasks: make(map[int]string),
	}
}

func (ce *CompositionExecutor) Execute(ctx context.Context, comp ToolComposition) ([]*BackgroundTask, error) {
	if err := comp.Validate(); err != nil {
		return nil, err
	}
	var results []*BackgroundTask
	for i, step := range comp.Steps {
		taskID, err := ce.be.Start(ctx, step.ToolName, step.Args)
		if err != nil {
			return results, err
		}
		ce.tasks[i] = taskID

		task, err := ce.be.WaitFor(taskID, 5*time.Minute)
		if err != nil {
			return results, err
		}
		results = append(results, task)

		if task.Status == TaskFailed {
			return results, fmt.Errorf("step %d (%s) failed: %v", i, step.ToolName, task.Error)
		}
	}
	return results, nil
}

func (ce *CompositionExecutor) GetStepTask(stepIndex int) (*BackgroundTask, error) {
	taskID, ok := ce.tasks[stepIndex]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return ce.be.Get(taskID)
}
