package tool

import (
	"context"
)

// ToolStreamItem is one item in a tool's streaming output.
// Stream invariant: [Progress*, Terminal] — zero or more Progress items
// followed by exactly one Terminal item.
type ToolStreamItem struct {
	Progress *ToolProgress
	Terminal *ToolTerminal
}

// ToolProgress carries intermediate output during tool execution.
// For example, bash stdout lines, long-running task status, or partial responses.
type ToolProgress struct {
	Text string `json:"text"`
}

// ToolTerminal carries the final result of a tool execution.
type ToolTerminal struct {
	Result string `json:"result"`
	Error  error  `json:"-"`
}

// IStreamingTool is an optional interface that tools can implement to
// provide streaming progress updates during execution. Tools that don't
// implement this interface are automatically wrapped by StreamAdapter.
type IStreamingTool interface {
	ITool
	ExecuteStream(ctx context.Context, args map[string]any) <-chan ToolStreamItem
}

// StreamAdapter wraps a non-streaming ITool into an IStreamingTool.
// The returned channel emits a single Terminal item after Execute completes.
func StreamAdapter(tool ITool) IStreamingTool {
	return &streamAdapter{tool: tool}
}

type streamAdapter struct {
	tool ITool
}

func (a *streamAdapter) Name() string                { return a.tool.Name() }
func (a *streamAdapter) Description() string         { return a.tool.Description() }
func (a *streamAdapter) InputSchema() map[string]any { return a.tool.InputSchema() }

func (a *streamAdapter) Execute(ctx context.Context, args map[string]any) (Result, error) {
	return a.tool.Execute(ctx, args)
}

func (a *streamAdapter) ExecuteStream(ctx context.Context, args map[string]any) <-chan ToolStreamItem {
	ch := make(chan ToolStreamItem, 1)
	go func() {
		defer close(ch)
		result, err := a.tool.Execute(ctx, args)
		ch <- ToolStreamItem{Terminal: &ToolTerminal{
			Result: result.Text,
			Error:  err,
		}}
	}()
	return ch
}

// AsStreaming returns a streaming wrapper for any tool. If the tool already
// implements IStreamingTool, it is returned as-is. Otherwise, a StreamAdapter
// is created.
func AsStreaming(t ITool) IStreamingTool {
	if st, ok := t.(IStreamingTool); ok {
		return st
	}
	return StreamAdapter(t)
}
