package llm

import (
	"context"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// MockGateway deterministic responses for local/dev/demo.
type MockGateway struct{}

func NewMock() *MockGateway { return &MockGateway{} }

func (m *MockGateway) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	return m.GenerateStream(ctx, req, nil)
}

func (m *MockGateway) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(port.StreamDelta)) (*port.ChatResponse, error) {
	_ = ctx
	last := ""
	if len(req.Messages) > 0 {
		last = req.Messages[len(req.Messages)-1].Content
	}
	// after tool results, finish
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "tool" {
			content := "已根据工具结果完成处理。\n\n" + truncate(req.Messages[i].Content, 400)
			if onDelta != nil {
				onDelta(port.StreamDelta{Type: "text", Text: content})
			}
			return &port.ChatResponse{Content: content, TotalTokens: 80}, nil
		}
	}
	lower := strings.ToLower(last)
	var content string
	switch {
	case strings.Contains(lower, "list") || strings.Contains(last, "列出") || strings.Contains(last, "文件") ||
		strings.Contains(last, "glob") || strings.Contains(lower, "files"):
		content = `{"name":"glob","args":{"pattern":"**/*"}}`
	case strings.Contains(lower, "grep") || strings.Contains(last, "搜索") || strings.Contains(last, "查找"):
		content = `{"name":"grep","args":{"pattern":"func","glob":"*.go"}}`
	case strings.Contains(lower, "read") || strings.Contains(last, "读取") || strings.Contains(last, "读"):
		content = `{"name":"read_file","args":{"path":"README.md"}}`
	case strings.Contains(lower, "write") || strings.Contains(last, "写入") || strings.Contains(last, "创建文件"):
		content = `{"name":"write_file","args":{"path":"hello.txt","content":"hello from code-agent\n"}}`
	case strings.Contains(lower, "edit") || strings.Contains(last, "修改"):
		content = `{"name":"edit_file","args":{"path":"hello.txt","old_string":"hello","new_string":"hi"}}`
	case strings.Contains(lower, "bash") || strings.Contains(last, "命令") || strings.Contains(last, "执行"):
		content = `{"name":"bash","args":{"command":"echo code-agent-ok"}}`
	case strings.Contains(lower, "remember") || strings.Contains(last, "记住") || strings.Contains(lower, "memory_save"):
		content = `{"name":"memory_save","args":{"content":"user prefers concise answers","scope":"user","category":"pref","importance":80}}`
	case strings.Contains(lower, "recall") || strings.Contains(last, "回忆") || strings.Contains(lower, "memory_search"):
		content = `{"name":"memory_search","args":{"query":"prefer"}}`
	case strings.Contains(lower, "delegate") || strings.Contains(last, "并行") || strings.Contains(last, "子代理") || strings.Contains(lower, "subagent"):
		content = `{"name":"delegate","args":{"tasks":[{"prompt":"List top-level files with glob","role":"explore","id":"e1"},{"prompt":"Search for package main with grep","role":"explore","id":"e2"}]}}`
	default:
		content = "Code-Agent mock 模式就绪。可试：列出文件 / 读取 README / 写入 hello.txt / 执行 echo。"
		if onDelta != nil {
			onDelta(port.StreamDelta{Type: "text", Text: content})
		}
		return &port.ChatResponse{Content: content, TotalTokens: 40}, nil
	}
	return &port.ChatResponse{Content: content, TotalTokens: 30}, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
