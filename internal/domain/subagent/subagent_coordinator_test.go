package subagent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- SubagentType tests ---

func TestSubagentTypeBuiltin(t *testing.T) {
	info, ok := ResolveSubagentType(SubagentTypeGeneralPurpose)
	if !ok {
		t.Error("general-purpose should be a built-in type")
	}
	if info.Label == "" {
		t.Error("label should not be empty")
	}
	if len(info.Tools) == 0 {
		t.Error("should have tools")
	}
}

func TestSubagentTypeUnknown(t *testing.T) {
	_, ok := ResolveSubagentType(SubagentType("nonexistent"))
	if ok {
		t.Error("nonexistent type should not resolve")
	}
}

func TestBuiltinSubagentTypesCount(t *testing.T) {
	types := BuiltinSubagentTypes()
	if len(types) != 5 {
		t.Errorf("want 5 built-in types, got %d", len(types))
	}
	for _, tp := range types {
		if !tp.Builtin {
			t.Errorf("type %s should be built-in", tp.Type)
		}
	}
}

// --- SubagentInput tests ---

func TestSubagentInputValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   SubagentInput
		wantErr bool
	}{
		{
			name:    "empty prompt",
			input:   SubagentInput{},
			wantErr: true,
		},
		{
			name: "valid with defaults",
			input: SubagentInput{
				Prompt: "do something",
			},
			wantErr: false,
		},
		{
			name: "worktree + cwd conflict",
			input: SubagentInput{
				Prompt:    "do something",
				Isolation: IsolationWorktree,
				CWD:       "/some/path",
			},
			wantErr: true,
		},
		{
			name: "explicit type",
			input: SubagentInput{
				Prompt:       "explore code",
				SubagentType: SubagentTypeExplore,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- SubagentStatus tests ---

func TestSubagentStatusIsTerminal(t *testing.T) {
	if SubagentStatusPending.IsTerminal() {
		t.Error("pending should not be terminal")
	}
	if SubagentStatusRunning.IsTerminal() {
		t.Error("running should not be terminal")
	}
	if !SubagentStatusCompleted.IsTerminal() {
		t.Error("completed should be terminal")
	}
	if !SubagentStatusFailed.IsTerminal() {
		t.Error("failed should be terminal")
	}
	if !SubagentStatusCancelled.IsTerminal() {
		t.Error("cancelled should be terminal")
	}
}

// --- SubagentCoordinator tests ---

func TestCoordinatorSpawnAndComplete(t *testing.T) {
	coordinator := NewSubagentCoordinator(DefaultRunner())
	ctx := context.Background()

	handle, err := coordinator.Spawn(ctx, SubagentInput{
		Prompt:      "test task",
		Description: "test",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if handle.Status != SubagentStatusPending {
		t.Errorf("initial status = %v, want pending", handle.Status)
	}

	output, err := coordinator.Wait(ctx, handle.TaskID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if output.Status != SubagentStatusCompleted {
		t.Errorf("output status = %v, want completed", output.Status)
	}
	if output.TokensUsed == 0 {
		t.Error("should have used tokens")
	}
}

func TestCoordinatorConcurrentLimit(t *testing.T) {
	var mu sync.Mutex
	active := 0
	runner := func(ctx context.Context, input SubagentInput) (SubagentOutput, error) {
		mu.Lock()
		active++
		current := active
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		if current > 3 {
			return SubagentOutput{}, context.DeadlineExceeded
		}
		return SubagentOutput{
			TaskID:     input.TaskID,
			Status:     SubagentStatusCompleted,
			Result:     "ok",
			TokensUsed: 10,
		}, nil
	}

	coordinator := NewSubagentCoordinator(runner, WithMaxConcurrent(3))
	ctx := context.Background()

	// Spawn 5 tasks - 3 should start immediately
	var wg sync.WaitGroup
	results := make([]SubagentOutput, 5)
	var errs []error

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			handle, err := coordinator.Spawn(ctx, SubagentInput{
				Prompt:      "test",
				Description: "test",
				TaskID:      "task-" + itoa(idx),
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			out, err := coordinator.Wait(ctx, handle.TaskID)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			} else {
				results[idx] = out
			}
		}(i)
	}
	wg.Wait()

	running := coordinator.RunningCount()
	if running < 0 {
		t.Errorf("running count should be non-negative, got %d", running)
	}
}

func TestCoordinatorCancel(t *testing.T) {
	slowRunner := func(ctx context.Context, input SubagentInput) (SubagentOutput, error) {
		select {
		case <-ctx.Done():
			return SubagentOutput{}, ctx.Err()
		case <-time.After(5 * time.Second):
			return SubagentOutput{Status: SubagentStatusCompleted}, nil
		}
	}

	coordinator := NewSubagentCoordinator(slowRunner)
	ctx := context.Background()

	handle, err := coordinator.Spawn(ctx, SubagentInput{
		Prompt:      "slow task",
		Description: "slow",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	err = coordinator.Cancel(handle.TaskID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	output, err := coordinator.Wait(ctx, handle.TaskID)
	if err == nil && output.Status != SubagentStatusCancelled {
		t.Errorf("output status = %v, want cancelled", output.Status)
	}
}

func TestCoordinatorList(t *testing.T) {
	coordinator := NewSubagentCoordinator(DefaultRunner())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := coordinator.Spawn(ctx, SubagentInput{
			Prompt:      "task",
			Description: "task",
			TaskID:      "t-" + itoa(i),
		})
		if err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}

	handles := coordinator.List()
	if len(handles) != 3 {
		t.Errorf("want 3 handles, got %d", len(handles))
	}
}

func TestCoordinatorCleanup(t *testing.T) {
	coordinator := NewSubagentCoordinator(DefaultRunner())
	ctx := context.Background()

	_, err := coordinator.Spawn(ctx, SubagentInput{
		Prompt:      "task",
		Description: "task",
		TaskID:      "cleanup-test",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Wait for completion
	_, err = coordinator.Wait(ctx, "cleanup-test")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Cleanup with 0 max age - should remove completed tasks
	removed := coordinator.Cleanup(0)
	if removed < 1 {
		t.Errorf("want at least 1 removed, got %d", removed)
	}

	handles := coordinator.List()
	for _, h := range handles {
		if h.TaskID == "cleanup-test" {
			t.Error("cleanup-test should have been removed")
		}
	}
}

func TestCoordinatorCallbacks(t *testing.T) {
	var mu sync.Mutex
	completedCount := 0
	errorCount := 0

	coordinator := NewSubagentCoordinator(DefaultRunner(),
		WithOnComplete(func(output SubagentOutput) {
			mu.Lock()
			completedCount++
			mu.Unlock()
		}),
		WithOnError(func(err error) {
			mu.Lock()
			errorCount++
			mu.Unlock()
		}),
	)

	ctx := context.Background()
	_, err := coordinator.Spawn(ctx, SubagentInput{
		Prompt:      "task",
		Description: "task",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Wait a bit for the task to complete
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if completedCount != 1 {
		t.Errorf("completedCount = %d, want 1", completedCount)
	}
	if errorCount != 0 {
		t.Errorf("errorCount = %d, want 0", errorCount)
	}
}

func TestCoordinatorEmptyPromptRejected(t *testing.T) {
	coordinator := NewSubagentCoordinator(DefaultRunner())
	_, err := coordinator.Spawn(context.Background(), SubagentInput{})
	if err == nil {
		t.Error("empty prompt should be rejected")
	}
}

func TestCoordinatorGetHandleNotFound(t *testing.T) {
	coordinator := NewSubagentCoordinator(DefaultRunner())
	_, err := coordinator.GetHandle("nonexistent")
	if err != ErrTaskNotFound {
		t.Errorf("want ErrTaskNotFound, got %v", err)
	}
}

// --- BuildSpawnContext tests ---

func TestBuildSpawnContext(t *testing.T) {
	parent := ParentContext{
		SessionID: "parent-1",
		Metadata:  map[string]string{"env": "test"},
	}
	input := SubagentInput{
		Prompt: "task",
	}
	result := BuildSpawnContext(parent, input)
	if result.ParentSession != "parent-1" {
		t.Errorf("parent session = %q, want parent-1", result.ParentSession)
	}
	if result.Metadata["env"] != "test" {
		t.Error("metadata should be inherited")
	}
}

func TestBuildSpawnContextPreservesExplicit(t *testing.T) {
	parent := ParentContext{
		SessionID: "parent-1",
		Metadata:  map[string]string{"env": "test"},
	}
	input := SubagentInput{
		Prompt:      "task",
		ParentSession: "explicit-parent",
		Metadata:     map[string]string{"custom": "value"},
	}
	result := BuildSpawnContext(parent, input)
	if result.ParentSession != "explicit-parent" {
		t.Errorf("explicit parent = %q, want explicit-parent", result.ParentSession)
	}
	if result.Metadata["custom"] != "value" {
		t.Error("explicit metadata should be preserved")
	}
	if result.Metadata["env"] != "test" {
		t.Error("parent metadata should be inherited")
	}
}

// --- DefaultRunner tests ---

func TestDefaultRunner(t *testing.T) {
	runner := DefaultRunner()
	output, err := runner(context.Background(), SubagentInput{
		TaskID:      "test-1",
		Description: "test",
		Model:       "grok-3",
	})
	if err != nil {
		t.Fatalf("DefaultRunner: %v", err)
	}
	if output.Status != SubagentStatusCompleted {
		t.Errorf("status = %v, want completed", output.Status)
	}
	if output.Model != "grok-3" {
		t.Errorf("model = %q, want grok-3", output.Model)
	}
	if output.TokensUsed == 0 {
		t.Error("tokens used should be > 0")
	}
}

func TestDefaultRunnerContextCancelled(t *testing.T) {
	runner := DefaultRunner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner(ctx, SubagentInput{
		TaskID: "test-2",
	})
	if err == nil {
		t.Error("cancelled context should produce error")
	}
}

// Test helper
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i >= 10 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	pos--
	buf[pos] = byte('0' + i)
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
