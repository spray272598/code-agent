package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CodeModeTool wraps all registered tools behind a single "run_code" entry point,
// inspired by DeepSeek Harness's Code Mode. The model calls run_code with a
// script; the tool parses the script for tool invocations and dispatches them.
//
// Security: only explicitly registered tools are callable; the script is
// sandboxed to the workspace.
type CodeModeTool struct {
	registry   *MapRegistry
	scriptTool ITool // the inner "run_code" tool exposed to the model
}

// NewCodeModeTool creates a CodeModeTool wrapping the given registry.
func NewCodeModeTool(reg *MapRegistry) *CodeModeTool {
	cm := &CodeModeTool{registry: reg}
	cm.scriptTool = &runCodeTool{cm: cm}
	return cm
}

// ScriptTool returns the single tool the model sees (run_code).
func (cm *CodeModeTool) ScriptTool() ITool {
	return cm.scriptTool
}

// SDKPrompt generates a document describing available tools for the model's
// system prompt when in Code Mode.
func (cm *CodeModeTool) SDKPrompt() string {
	var b strings.Builder
	b.WriteString("# Available Tools (SDK)\n\n")
	b.WriteString("You have access to the following tools. Call them via run_code:\n")
	b.WriteString("```run_code\ntool_name({\"arg\": \"value\"})\n```\n\n")
	for _, info := range cm.registry.ListInfo() {
		b.WriteString(fmt.Sprintf("## %s\n%s\n", info.Name, info.Description))
		schema, _ := json.MarshalIndent(info.InputSchema, "", "  ")
		b.WriteString(fmt.Sprintf("Schema: `%s`\n\n", schema))
	}
	return b.String()
}

// runCodeTool is the single entry-point tool.
type runCodeTool struct {
	cm *CodeModeTool
}

func (t *runCodeTool) Name() string { return "run_code" }
func (t *runCodeTool) Description() string {
	return "Execute a code script that invokes registered tools. Format: tool_name({args})"
}

func (t *runCodeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "Script with tool invocations: tool_name({\"arg\":\"val\"})",
			},
		},
		"required": []string{"code"},
	}
}

func (t *runCodeTool) Execute(ctx context.Context, args map[string]any) (Result, error) {
	code, _ := args["code"].(string)
	if code == "" {
		return Result{Text: "code required", IsError: true}, nil
	}

	invocations := parseCodeInvocations(code)
	if len(invocations) == 0 {
		return Result{Text: "no tool invocations found in code"}, nil
	}

	var results []string
	for _, inv := range invocations {
		tool := t.cm.registry.Get(inv.Name)
		if tool == nil {
			results = append(results, fmt.Sprintf("[%s] error: tool not found", inv.Name))
			continue
		}
		res, err := tool.Execute(ctx, inv.Args)
		if err != nil {
			results = append(results, fmt.Sprintf("[%s] error: %v", inv.Name, err))
			continue
		}
		output := res.Text
		if len(output) > 2000 {
			output = output[:2000] + "...(truncated)"
		}
		results = append(results, fmt.Sprintf("[%s] %s", inv.Name, output))
	}
	return Result{Text: strings.Join(results, "\n")}, nil
}

// toolInvocation represents a parsed tool call from code.
type toolInvocation struct {
	Name string
	Args map[string]any
}

// parseCodeInvocations extracts tool calls from code like:
//
//	read_file({"path": "main.go"})
//	bash({"command": "ls"})
func parseCodeInvocations(code string) []toolInvocation {
	var invocations []toolInvocation
	lines := strings.Split(code, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		inv := parseSingleInvocation(line)
		if inv != nil {
			invocations = append(invocations, *inv)
		}
	}
	return invocations
}

func parseSingleInvocation(line string) *toolInvocation {
	// Find first '(' to extract tool name
	parenIdx := strings.Index(line, "(")
	if parenIdx < 0 {
		return nil
	}
	name := strings.TrimSpace(line[:parenIdx])
	if name == "" {
		return nil
	}
	// Find matching ')'
	closeIdx := strings.LastIndex(line, ")")
	if closeIdx <= parenIdx {
		return nil
	}
	argsJSON := strings.TrimSpace(line[parenIdx+1 : closeIdx])
	if argsJSON == "" {
		return &toolInvocation{Name: name, Args: map[string]any{}}
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil
	}
	return &toolInvocation{Name: name, Args: args}
}
