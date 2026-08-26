package subagent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- SubagentRuntime tests ---

func TestSubagentRuntimeInheritance(t *testing.T) {
	parent := ParentContext{
		SessionID: "parent-1",
		ToolDefinitions: []ToolDefinition{
			{Name: "read_file", Description: "Read files"},
			{Name: "write_file", Description: "Write files"},
		},
		MCPServers: []MCPServerInfo{
			{Name: "github", Endpoint: "https://mcp.github.com", Tools: []string{"repos", "issues"}},
		},
	}

	runtime := NewSubagentRuntime(parent)

	tools := runtime.AvailableTools()
	if len(tools) != 2 {
		t.Errorf("want 2 tools, got %d", len(tools))
	}

	servers := runtime.AvailableMCPServers()
	if len(servers) != 1 {
		t.Errorf("want 1 MCP server, got %d", len(servers))
	}
	if servers[0].Name != "github" {
		t.Errorf("want github server, got %s", servers[0].Name)
	}

	if runtime.ParentSessionID() != "parent-1" {
		t.Errorf("parent session = %q, want parent-1", runtime.ParentSessionID())
	}
}

func TestSubagentRuntimeToolInheritance(t *testing.T) {
	runtime := NewSubagentRuntime(ParentContext{})
	runtime.InheritTool(ToolDefinition{Name: "grep", Description: "Search"})
	runtime.InheritTool(ToolDefinition{Name: "bash", Description: "Execute"})

	tools := runtime.AvailableTools()
	if len(tools) != 2 {
		t.Errorf("want 2 tools, got %d", len(tools))
	}

	runtime.InheritTool(ToolDefinition{Name: "grep", Description: "Search updated"})
	tools = runtime.AvailableTools()
	for _, td := range tools {
		if td.Name == "grep" && td.Description != "Search updated" {
			t.Error("tool should be overwritten")
		}
	}
}

func TestSubagentRuntimeProgressTracking(t *testing.T) {
	runtime := NewSubagentRuntime(ParentContext{})

	runtime.StartProgress("task-1")
	runtime.UpdateProgress("task-1", 100, 1, "working")

	select {
	case p := <-runtime.ProgressChan():
		if p.TaskID != "task-1" {
			t.Errorf("task ID = %q, want task-1", p.TaskID)
		}
		if p.TokensUsed != 100 {
			t.Errorf("tokens = %d, want 100", p.TokensUsed)
		}
		if p.Step != 1 {
			t.Errorf("step = %d, want 1", p.Step)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for progress")
	}

	runtime.EndProgress("task-1")
	total := runtime.TotalTokens()
	if total != 100 {
		t.Errorf("total tokens = %d, want 100", total)
	}
}

func TestSubagentRuntimeBuildPromptContext(t *testing.T) {
	parent := ParentContext{
		SessionID: "parent-1",
		ToolDefinitions: []ToolDefinition{
			{Name: "read_file", Description: "Read"},
		},
		MCPServers: []MCPServerInfo{
			{Name: "gh", Endpoint: "https://gh.com"},
		},
	}
	runtime := NewSubagentRuntime(parent)

	input := SubagentInput{
		Prompt:        "do something",
		Description:   "test task",
		ParentSession: "parent-1",
	}

	ctx := runtime.BuildPromptContext(input)
	if !contains(ctx, "test task") {
		t.Error("should contain description")
	}
	if !contains(ctx, "read_file") {
		t.Error("should contain tool name")
	}
	if !contains(ctx, "gh") {
		t.Error("should contain MCP server")
	}
	if !contains(ctx, "parent-1") {
		t.Error("should contain parent session")
	}
}

// --- SubagentTypeResolver tests ---

func TestSubagentTypeResolverBuiltin(t *testing.T) {
	resolver := NewSubagentTypeResolver()

	info, err := resolver.Validate(SubagentTypeGeneralPurpose)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if info.Type != SubagentTypeGeneralPurpose {
		t.Errorf("type = %v, want general-purpose", info.Type)
	}
	if len(info.Tools) == 0 {
		t.Error("should have tools")
	}
}

func TestSubagentTypeResolverUnknown(t *testing.T) {
	resolver := NewSubagentTypeResolver()
	_, err := resolver.Validate(SubagentType("nonexistent"))
	if err == nil {
		t.Error("unknown type should fail validation")
	}
}

func TestSubagentTypeResolverRegister(t *testing.T) {
	resolver := NewSubagentTypeResolver()
	customInfo := SubagentTypeInfo{
		Type:        SubagentType("custom-type"),
		Label:       "Custom",
		Description: "Custom subagent type",
		Tools:       []string{"custom_tool"},
	}
	resolver.Register(customInfo)

	info, err := resolver.Validate(SubagentType("custom-type"))
	if err != nil {
		t.Fatalf("Validate custom: %v", err)
	}
	if info.Label != "Custom" {
		t.Errorf("label = %q, want Custom", info.Label)
	}
}

func TestSubagentTypeResolverDescribe(t *testing.T) {
	resolver := NewSubagentTypeResolver()
	info, ok := resolver.Describe(SubagentTypeExplore)
	if !ok {
		t.Error("explore should be describable")
	}
	if info.Type != SubagentTypeExplore {
		t.Errorf("type = %v, want explore", info.Type)
	}

	_, ok = resolver.Describe(SubagentType("nonexistent"))
	if ok {
		t.Error("nonexistent should not be describable")
	}
}

func TestSubagentTypeResolverList(t *testing.T) {
	resolver := NewSubagentTypeResolver()
	infos := resolver.List()
	if len(infos) != 5 {
		t.Errorf("want 5 built-in types, got %d", len(infos))
	}
}

func TestSubagentTypeResolverValidateAndResolve(t *testing.T) {
	resolver := NewSubagentTypeResolver()

	input := &SubagentInput{
		Prompt: "test",
	}
	info, err := resolver.ValidateAndResolve(input)
	if err != nil {
		t.Fatalf("ValidateAndResolve: %v", err)
	}
	if info.Type != SubagentTypeGeneralPurpose {
		t.Errorf("default type = %v, want general-purpose", info.Type)
	}
	if input.SubagentType != SubagentTypeGeneralPurpose {
		t.Errorf("input type should be defaulted, got %v", input.SubagentType)
	}

	input2 := &SubagentInput{
		Prompt:       "test",
		SubagentType: SubagentTypeVerify,
	}
	info2, err := resolver.ValidateAndResolve(input2)
	if err != nil {
		t.Fatalf("ValidateAndResolve: %v", err)
	}
	if info2.Type != SubagentTypeVerify {
		t.Errorf("type = %v, want verify", info2.Type)
	}
}

// --- Integration: Runtime + Coordinator ---

func TestRuntimeCoordinatorIntegration(t *testing.T) {
	parent := ParentContext{
		SessionID: "parent-int-1",
		ToolDefinitions: []ToolDefinition{
			{Name: "read_file", Description: "Read"},
		},
	}
	runtime := NewSubagentRuntime(parent)

	runner := func(ctx context.Context, input SubagentInput) (SubagentOutput, error) {
		runtime.StartProgress(input.TaskID)
		runtime.UpdateProgress(input.TaskID, 50, 1, "executing")

		tools := runtime.AvailableTools()
		if len(tools) == 0 {
			return SubagentOutput{}, fmt.Errorf("no tools available")
		}

		runtime.UpdateProgress(input.TaskID, 100, 2, "complete")
		runtime.EndProgress(input.TaskID)

		return SubagentOutput{
			TaskID:     input.TaskID,
			Status:     SubagentStatusCompleted,
			Result:     "Task completed successfully",
			TokensUsed: 100,
		}, nil
	}

	coordinator := NewSubagentCoordinator(runner)
	ctx := context.Background()

	handle, err := coordinator.Spawn(ctx, SubagentInput{
		Prompt:        "test task",
		Description:   "integration test",
		SubagentType:  SubagentTypeGeneralPurpose,
		ParentSession: "parent-int-1",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	output, err := coordinator.Wait(ctx, handle.TaskID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if output.Status != SubagentStatusCompleted {
		t.Errorf("status = %v, want completed", output.Status)
	}
	if output.TokensUsed != 100 {
		t.Errorf("tokens = %d, want 100", output.TokensUsed)
	}
	// TotalTokens accumulates across all updates (50 + 100 = 150)
	if runtime.TotalTokens() != 150 {
		t.Errorf("runtime tokens = %d, want 150", runtime.TotalTokens())
	}
}

// --- Progress Channel tests ---

func TestProgressChannelBackpressure(t *testing.T) {
	runtime := NewSubagentRuntime(ParentContext{})
	runtime.StartProgress("task-1")

	for i := 0; i < 200; i++ {
		runtime.UpdateProgress("task-1", int64(i*10), i, "progress")
	}

	runtime.EndProgress("task-1")

	total := runtime.TotalTokens()
	if total <= 0 {
		t.Error("should have accumulated tokens despite backpressure")
	}
}

// --- SubagentProgress tests ---

func TestSubagentProgressTimestamp(t *testing.T) {
	p := SubagentProgress{
		TaskID:    "test",
		Timestamp: time.Now(),
	}
	if p.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

// Verify fmt and sync imports are used
var _ = fmt.Sprintf
var _ = sync.Mutex{}
