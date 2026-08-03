package coding

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/observability"
)

// MemoryContext is process-wide service + per-request identity (set before each agent run).
type MemoryContext struct {
	mu        sync.RWMutex
	UserID    string
	ProjectID string
	Svc       *memory.Service
}

func (c *MemoryContext) Bind(userID, projectID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.UserID = userID
	c.ProjectID = projectID
	c.mu.Unlock()
}

func (c *MemoryContext) identity() (userID, projectID string) {
	if c == nil {
		return "", ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.UserID, c.ProjectID
}

// MemorySaveTool persists a long-term memory fact.
type MemorySaveTool struct {
	Ctx *MemoryContext
}

func NewMemorySave(ctx *MemoryContext) *MemorySaveTool { return &MemorySaveTool{Ctx: ctx} }

func (t *MemorySaveTool) Name() string { return "memory_save" }
func (t *MemorySaveTool) Description() string {
	return "Save long-term memory. Args: content (required), scope=user|project, category?, importance? (1-100), projectId?"
}
func (t *MemorySaveTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"content":    map[string]any{"type": "string"},
		"scope":      map[string]any{"type": "string"},
		"category":   map[string]any{"type": "string"},
		"importance": map[string]any{"type": "integer"},
		"projectId":  map[string]any{"type": "string"},
	}, "required": []string{"content"}}
}

func (t *MemorySaveTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	if t.Ctx == nil || t.Ctx.Svc == nil {
		return tool.Result{Text: "memory service unavailable", IsError: true}, nil
	}
	content, _ := args["content"].(string)
	scope := memport.ScopeUser
	if s, ok := args["scope"].(string); ok && strings.EqualFold(s, "project") {
		scope = memport.ScopeProject
	}
	cat, _ := args["category"].(string)
	proj, _ := args["projectId"].(string)
	imp := 50
	switch v := args["importance"].(type) {
	case float64:
		imp = int(v)
	case int:
		imp = v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			imp = n
		}
	}
	userID, defaultProj := t.Ctx.identity()
	if proj == "" {
		proj = defaultProj
	}
	item := &memport.MemoryItem{
		UserID: userID, ProjectID: proj, Scope: scope,
		Category: cat, Content: content, Importance: imp, Source: "tool",
	}
	if err := t.Ctx.Svc.Save(ctx, item); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	observability.Global.MemoryWrites.Add(1)
	return tool.Result{Text: fmt.Sprintf("saved memory scope=%s category=%s: %s", scope, item.Category, truncate(content, 120))}, nil
}

// MemorySearchTool retrieves memories by keyword.
type MemorySearchTool struct {
	Ctx *MemoryContext
}

func NewMemorySearch(ctx *MemoryContext) *MemorySearchTool { return &MemorySearchTool{Ctx: ctx} }

func (t *MemorySearchTool) Name() string { return "memory_search" }
func (t *MemorySearchTool) Description() string {
	return "Search long-term memories. Args: query (required), limit?, scope?=user|project|all"
}
func (t *MemorySearchTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string"},
		"limit": map[string]any{"type": "integer"},
		"scope": map[string]any{"type": "string"},
	}, "required": []string{"query"}}
}

func (t *MemorySearchTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	if t.Ctx == nil || t.Ctx.Svc == nil {
		return tool.Result{Text: "memory service unavailable", IsError: true}, nil
	}
	q, _ := args["query"].(string)
	limit := 10
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	userID, projectID := t.Ctx.identity()
	items, err := t.Ctx.Svc.Search(ctx, userID, projectID, q, limit)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	observability.Global.MemoryReads.Add(1)
	if len(items) == 0 {
		return tool.Result{Text: "(no memories matched)"}, nil
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString(fmt.Sprintf("- id=%d [%s|%s|imp=%d] %s\n", it.ID, it.Scope, it.Category, it.Importance, it.Content))
	}
	return tool.Result{Text: b.String()}, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
