package coding

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// SearchReplaceTool provides grok-build style search-and-replace editing.
// It enforces exact string matching with read-before-write semantics.
type SearchReplaceTool struct{ ws *Workspace }

func NewSearchReplace(ws *Workspace) *SearchReplaceTool { return &SearchReplaceTool{ws: ws} }
func (t *SearchReplaceTool) Name() string               { return "search_replace" }
func (t *SearchReplaceTool) Description() string {
	return "Edit file with exact string replacement. Args: path, old_string, new_string; optional replace_all(bool). old_string must match exactly once unless replace_all is true."
}

func (t *SearchReplaceTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path":        map[string]any{"type": "string"},
		"old_string":  map[string]any{"type": "string", "description": "exact text to find (must be unique unless replace_all)"},
		"new_string":  map[string]any{"type": "string", "description": "replacement text (empty string to delete)"},
		"replace_all": map[string]any{"type": "boolean", "description": "replace every occurrence (default false)"},
	}, "required": []string{"path", "old_string", "new_string"}}
}

func (t *SearchReplaceTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	oldS, _ := args["old_string"].(string)
	newS, _ := args["new_string"].(string)
	replaceAll := boolArg(args, "replace_all")

	abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), path)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	content := string(b)

	if oldS == "" {
		return tool.Result{Text: "old_string empty", IsError: true}, nil
	}

	// Normalize CRLF if file uses LF
	if !strings.Contains(content, "\r\n") && strings.Contains(oldS, "\r\n") {
		oldS = strings.ReplaceAll(oldS, "\r\n", "\n")
	}

	count := strings.Count(content, oldS)
	if count == 0 {
		// Idempotent: check if new_string is already present
		if newS != "" && strings.Contains(content, newS) {
			return tool.Result{Text: "search_replace: change already applied (idempotent)"}, nil
		}
		return tool.Result{Text: "old_string not found", IsError: true}, nil
	}

	if !replaceAll && count > 1 {
		return tool.Result{Text: fmt.Sprintf("old_string matches %d times; make it unique or set replace_all=true", count), IsError: true}, nil
	}

	var out string
	if replaceAll {
		out = strings.ReplaceAll(content, oldS, newS)
	} else {
		out = strings.Replace(content, oldS, newS, 1)
	}

	if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	return tool.Result{Text: fmt.Sprintf("replaced %d occurrence(s)", count)}, nil
}

// InsertAtLineTool inserts content at a specific line number.
type InsertAtLineTool struct{ ws *Workspace }

func NewInsertAtLine(ws *Workspace) *InsertAtLineTool { return &InsertAtLineTool{ws: ws} }
func (t *InsertAtLineTool) Name() string              { return "insert_at_line" }
func (t *InsertAtLineTool) Description() string {
	return "Insert content at a specific line. Args: path, line(int), content(string). Lines are 1-indexed."
}

func (t *InsertAtLineTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path":    map[string]any{"type": "string"},
		"line":    map[string]any{"type": "integer", "description": "line number (1-indexed)"},
		"content": map[string]any{"type": "string", "description": "content to insert"},
	}, "required": []string{"path", "line", "content"}}
}

func (t *InsertAtLineTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	line := intArg(args, "line", 0)
	content, _ := args["content"].(string)

	if line <= 0 {
		return tool.Result{Text: "line must be > 0", IsError: true}, nil
	}

	abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), path)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	lines := strings.Split(string(b), "\n")
	if line > len(lines)+1 {
		return tool.Result{Text: fmt.Sprintf("line %d exceeds file length %d", line, len(lines)), IsError: true}, nil
	}

	// Insert at position (1-indexed)
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:line-1]...)
	newLines = append(newLines, content)
	newLines = append(newLines, lines[line-1:]...)

	out := strings.Join(newLines, "\n")
	if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	return tool.Result{Text: fmt.Sprintf("inserted at line %d", line)}, nil
}

// DeleteRangeTool deletes a range of lines.
type DeleteRangeTool struct{ ws *Workspace }

func NewDeleteRange(ws *Workspace) *DeleteRangeTool { return &DeleteRangeTool{ws: ws} }
func (t *DeleteRangeTool) Name() string             { return "delete_range" }
func (t *DeleteRangeTool) Description() string {
	return "Delete a range of lines. Args: path, start_line(int), end_line(int). Lines are 1-indexed, inclusive."
}

func (t *DeleteRangeTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path":       map[string]any{"type": "string"},
		"start_line": map[string]any{"type": "integer", "description": "start line (1-indexed, inclusive)"},
		"end_line":   map[string]any{"type": "integer", "description": "end line (1-indexed, inclusive)"},
	}, "required": []string{"path", "start_line", "end_line"}}
}

func (t *DeleteRangeTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	startLine := intArg(args, "start_line", 0)
	endLine := intArg(args, "end_line", 0)

	if startLine <= 0 || endLine < startLine {
		return tool.Result{Text: "invalid line range", IsError: true}, nil
	}

	abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), path)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	lines := strings.Split(string(b), "\n")
	if startLine > len(lines) {
		return tool.Result{Text: fmt.Sprintf("start_line %d exceeds file length %d", startLine, len(lines)), IsError: true}, nil
	}

	// Clamp end_line
	if endLine > len(lines) {
		endLine = len(lines)
	}

	// Delete range (1-indexed, inclusive)
	newLines := make([]string, 0, len(lines)-(endLine-startLine+1))
	newLines = append(newLines, lines[:startLine-1]...)
	newLines = append(newLines, lines[endLine:]...)

	out := strings.Join(newLines, "\n")
	if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}

	return tool.Result{Text: fmt.Sprintf("deleted lines %d-%d", startLine, endLine)}, nil
}
