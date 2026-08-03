package coding

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

// Workspace resolves paths under root with sandbox.
type Workspace struct {
	Root string
}

func NewWorkspace(root string) *Workspace {
	if root == "" {
		root = "./workspace"
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	_ = os.MkdirAll(root, 0o755)
	return &Workspace{Root: root}
}

func (w *Workspace) Resolve(rel string) (string, error) {
	if rel == "" || rel == "." {
		return w.Root, nil
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) {
		// still must be under root
		abs := clean
		rel2, err := filepath.Rel(w.Root, abs)
		if err != nil || strings.HasPrefix(rel2, "..") {
			return "", fmt.Errorf("path outside workspace: %s", rel)
		}
		return abs, nil
	}
	full := filepath.Join(w.Root, clean)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel2, err := filepath.Rel(w.Root, abs)
	if err != nil || strings.HasPrefix(rel2, "..") {
		return "", fmt.Errorf("path outside workspace: %s", rel)
	}
	return abs, nil
}

// --- ReadFile ---

type ReadFileTool struct{ ws *Workspace }

func NewReadFile(ws *Workspace) *ReadFileTool { return &ReadFileTool{ws: ws} }
func (t *ReadFileTool) Name() string          { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "Read a file under the project workspace. Args: path (required), offset?, limit?"
}
func (t *ReadFileTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"},
	}, "required": []string{"path"}}
}
func (t *ReadFileTool) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	abs, err := t.ws.Resolve(path)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	text := string(b)
	if len([]rune(text)) > 8000 {
		text = common.TruncateRunes(text, 8000)
	}
	return tool.Result{Text: text}, nil
}

// --- WriteFile ---

type WriteFileTool struct{ ws *Workspace }

func NewWriteFile(ws *Workspace) *WriteFileTool { return &WriteFileTool{ws: ws} }
func (t *WriteFileTool) Name() string           { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Write full file content. Args: path, content"
}
func (t *WriteFileTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"},
	}, "required": []string{"path", "content"}}
}
func (t *WriteFileTool) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	abs, err := t.ws.Resolve(path)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	return tool.Result{Text: fmt.Sprintf("wrote %s (%d bytes)", path, len(content))}, nil
}

// --- EditFile (search_replace) ---

type EditFileTool struct{ ws *Workspace }

func NewEditFile(ws *Workspace) *EditFileTool { return &EditFileTool{ws: ws} }
func (t *EditFileTool) Name() string          { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Edit file by exact search_replace. Args: path, old_string, new_string"
}
func (t *EditFileTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"},
		"old_string": map[string]any{"type": "string"},
		"new_string": map[string]any{"type": "string"},
	}, "required": []string{"path", "old_string", "new_string"}}
}
func (t *EditFileTool) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	oldS, _ := args["old_string"].(string)
	newS, _ := args["new_string"].(string)
	abs, err := t.ws.Resolve(path)
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
	count := strings.Count(content, oldS)
	if count == 0 {
		return tool.Result{Text: "old_string not found", IsError: true}, nil
	}
	if count > 1 {
		return tool.Result{Text: fmt.Sprintf("old_string matches %d times; make it unique", count), IsError: true}, nil
	}
	content = strings.Replace(content, oldS, newS, 1)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	return tool.Result{Text: fmt.Sprintf("edited %s (1 replacement)", path)}, nil
}

// --- Glob ---

type GlobTool struct{ ws *Workspace }

func NewGlob(ws *Workspace) *GlobTool { return &GlobTool{ws: ws} }
func (t *GlobTool) Name() string      { return "glob" }
func (t *GlobTool) Description() string {
	return "Find files by glob pattern under workspace. Args: pattern (e.g. **/*.go), path? (subdir)"
}
func (t *GlobTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
	}, "required": []string{"pattern"}}
}
func (t *GlobTool) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	pattern, _ := args["pattern"].(string)
	sub, _ := args["path"].(string)
	root, err := t.ws.Resolve(sub)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	// simple ** support via Walk
	var matches []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		ok, _ := pathMatch(pattern, rel)
		if ok {
			matches = append(matches, rel)
		}
		if len(matches) >= 200 {
			return fs.SkipAll
		}
		return nil
	})
	if len(matches) == 0 {
		return tool.Result{Text: "(no matches)"}, nil
	}
	return tool.Result{Text: strings.Join(matches, "\n")}, nil
}

func pathMatch(pattern, name string) (bool, error) {
	// filepath.Match doesn't support **; approximate
	p := strings.ReplaceAll(pattern, "**/*", "*")
	p = strings.ReplaceAll(p, "**/", "")
	p = strings.ReplaceAll(p, "**", "*")
	if strings.Contains(p, "/") {
		return filepath.Match(p, name)
	}
	base := filepath.Base(name)
	return filepath.Match(p, base)
}

// --- Grep ---

type GrepTool struct{ ws *Workspace }

func NewGrep(ws *Workspace) *GrepTool { return &GrepTool{ws: ws} }
func (t *GrepTool) Name() string      { return "grep" }
func (t *GrepTool) Description() string {
	return "Search file contents by regex. Args: pattern, path? (subdir), glob? (e.g. *.go)"
}
func (t *GrepTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
		"glob":    map[string]any{"type": "string"},
	}, "required": []string{"pattern"}}
}
func (t *GrepTool) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	pat, _ := args["pattern"].(string)
	sub, _ := args["path"].(string)
	gfilter, _ := args["glob"].(string)
	re, err := regexp.Compile(pat)
	if err != nil {
		return tool.Result{Text: "invalid regex: " + err.Error(), IsError: true}, nil
	}
	root, err := t.ws.Resolve(sub)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	var lines []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if gfilter != "" {
			ok, _ := filepath.Match(gfilter, filepath.Base(p))
			if !ok {
				return nil
			}
		}
		// skip huge/binary-ish
		info, _ := d.Info()
		if info != nil && info.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				lines = append(lines, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), i+1, common.TruncateRunes(line, 200)))
				if len(lines) >= 100 {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if len(lines) == 0 {
		return tool.Result{Text: "(no matches)"}, nil
	}
	return tool.Result{Text: strings.Join(lines, "\n")}, nil
}
