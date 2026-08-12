package coding

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/types/common"
)

// Workspace resolves paths under root with sandbox.
type Workspace struct {
	Root      string
	mu        sync.RWMutex
	sessionRT map[string]string // sessionID → override root
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
	return &Workspace{Root: root, sessionRT: make(map[string]string)}
}

// SetSessionRoot overrides the root for a specific session.
func (w *Workspace) SetSessionRoot(sessionID, path string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if path == "" {
		delete(w.sessionRT, sessionID)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	w.sessionRT[sessionID] = abs
}

// EffectiveRoot returns the root for a session (session override > global).
func (w *Workspace) EffectiveRoot(sessionID string) string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	if r, ok := w.sessionRT[sessionID]; ok {
		w.mu.RUnlock()
		return r
	}
	w.mu.RUnlock()
	return w.Root
}

func (w *Workspace) Resolve(rel string) (string, error) {
	return w.ResolveForSession("", rel)
}

// ResolveForSession resolves a path under the session-specific root.
func (w *Workspace) ResolveForSession(sessionID, rel string) (string, error) {
	root := w.EffectiveRoot(sessionID)
	if root == "" {
		return "", fmt.Errorf("workspace not configured")
	}
	if rel == "" || rel == "." {
		return root, nil
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) {
		rel2, err := filepath.Rel(root, clean)
		if err != nil || strings.HasPrefix(rel2, "..") {
			return "", fmt.Errorf("path outside workspace: %s", rel)
		}
		return clean, nil
	}
	full := filepath.Join(root, clean)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel2, err := filepath.Rel(root, abs)
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
func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), path)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	text := string(b)
	if len([]rune(text)) > common.ReadFileMaxRunes {
		text = common.TruncateRunes(text, common.ReadFileMaxRunes)
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
func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), path)
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

// --- EditFile (search_replace + multi-line + regex) ---

type EditFileTool struct{ ws *Workspace }

func NewEditFile(ws *Workspace) *EditFileTool { return &EditFileTool{ws: ws} }
func (t *EditFileTool) Name() string          { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Edit file: exact multi-line replace or regex. Args: path, old_string, new_string; optional regex(bool), replace_all(bool), count(int)"
}
func (t *EditFileTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path":        map[string]any{"type": "string"},
		"old_string":  map[string]any{"type": "string", "description": "exact text or regex pattern (multi-line ok)"},
		"new_string":  map[string]any{"type": "string"},
		"regex":       map[string]any{"type": "boolean", "description": "treat old_string as Go regexp"},
		"replace_all": map[string]any{"type": "boolean", "description": "replace every match (default false for exact, true for regex if count unset)"},
		"count":       map[string]any{"type": "integer", "description": "max replacements; 0 or omit = 1 for exact unless replace_all"},
	}, "required": []string{"path", "old_string", "new_string"}}
}
func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	path, _ := args["path"].(string)
	oldS, _ := args["old_string"].(string)
	newS, _ := args["new_string"].(string)
	useRegex := boolArg(args, "regex")
	replaceAll := boolArg(args, "replace_all")
	nLimit := intArg(args, "count", -1)

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

	var (
		out      string
		replaced int
	)
	if useRegex {
		re, err := regexp.Compile(oldS)
		if err != nil {
			return tool.Result{Text: "invalid regex: " + err.Error(), IsError: true}, nil
		}
		limit := nLimit
		if limit < 0 {
			if replaceAll {
				limit = -1
			} else {
				limit = 1
			}
		}
		if limit == 0 {
			limit = -1
		}
		// submatch indices for Expand ($1, $2…)
		all := re.FindAllStringSubmatchIndex(content, limit)
		if len(all) == 0 {
			return tool.Result{Text: "regex matched 0 times", IsError: true}, nil
		}
		out = content
		for i := len(all) - 1; i >= 0; i-- {
			sub := all[i]
			lo, hi := sub[0], sub[1]
			repl := string(re.ExpandString(nil, newS, content, sub))
			out = out[:lo] + repl + out[hi:]
			replaced++
		}
	} else {
		// exact multi-line string replace (old_string may contain \n)
		// normalize CRLF in needle only if file has LF
		needle := oldS
		if !strings.Contains(content, "\r\n") && strings.Contains(needle, "\r\n") {
			needle = strings.ReplaceAll(needle, "\r\n", "\n")
		}
		count := strings.Count(content, needle)
		if count == 0 {
			return tool.Result{Text: "old_string not found", IsError: true}, nil
		}
		limit := 1
		if replaceAll || nLimit == 0 {
			limit = -1
		} else if nLimit > 0 {
			limit = nLimit
		}
		if !replaceAll && nLimit < 0 && count > 1 {
			return tool.Result{Text: fmt.Sprintf("old_string matches %d times; make it unique or set replace_all=true", count), IsError: true}, nil
		}
		if limit < 0 {
			out = strings.ReplaceAll(content, needle, newS)
			replaced = count
		} else {
			out = strings.Replace(content, needle, newS, limit)
			if count < limit {
				replaced = count
			} else {
				replaced = limit
			}
		}
	}
	if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	mode := "exact"
	if useRegex {
		mode = "regex"
	}
	return tool.Result{Text: fmt.Sprintf("edited %s (%d %s replacement(s))", path, replaced, mode)}, nil
}

func boolArg(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, _ := strconv.ParseBool(t)
		return b
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return false
	}
}

func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(t)
		if err != nil {
			return def
		}
		return n
	default:
		return def
	}
}

// --- Glob ---

type GlobTool struct{ ws *Workspace }

func NewGlob(ws *Workspace) *GlobTool { return &GlobTool{ws: ws} }
func (t *GlobTool) Name() string      { return "glob" }
func (t *GlobTool) Description() string {
	return "Find files by glob (doublestar ** supported). Args: pattern (e.g. **/*.{go,md}), path? (subdir)"
}
func (t *GlobTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string"},
	}, "required": []string{"pattern"}}
}
func (t *GlobTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	pattern, _ := args["pattern"].(string)
	sub, _ := args["path"].(string)
	if pattern == "" {
		return tool.Result{Text: "pattern required", IsError: true}, nil
	}
	root, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), sub)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	// normalize pattern to slash form for doublestar
	pattern = filepath.ToSlash(pattern)
	// if pattern is not recursive and has no path sep, match basename anywhere
	if !strings.Contains(pattern, "/") && !strings.Contains(pattern, "**") {
		pattern = "**/" + pattern
	}
	var matches []string
	// Prefer doublestar.Glob on OS FS rooted at workspace subdir
	fsys := os.DirFS(root)
	found, err := doublestar.Glob(fsys, pattern)
	if err != nil {
		// fallback walk with PathMatch
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			ok, _ := doublestar.Match(pattern, rel)
			if ok {
				matches = append(matches, rel)
			}
			if len(matches) >= common.GlobMaxMatches {
				return fs.SkipAll
			}
			return nil
		})
	} else {
		for _, m := range found {
			// skip directories
			full := filepath.Join(root, filepath.FromSlash(m))
			st, err := os.Stat(full)
			if err != nil || st.IsDir() {
				continue
			}
			matches = append(matches, filepath.ToSlash(m))
			if len(matches) >= common.GlobMaxMatches {
				break
			}
		}
	}
	if len(matches) == 0 {
		return tool.Result{Text: "(no matches)"}, nil
	}
	return tool.Result{Text: strings.Join(matches, "\n")}, nil
}

// --- Grep ---

type GrepTool struct{ ws *Workspace }

func NewGrep(ws *Workspace) *GrepTool { return &GrepTool{ws: ws} }
func (t *GrepTool) Name() string      { return "grep" }
func (t *GrepTool) Description() string {
	return "Search file contents by regex. Args: pattern, path?, glob?, context|context_before|context_after (like -C/-B/-A)"
}
func (t *GrepTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"pattern":        map[string]any{"type": "string"},
		"path":           map[string]any{"type": "string"},
		"glob":           map[string]any{"type": "string"},
		"context":        map[string]any{"type": "integer", "description": "lines of context before and after (-C)"},
		"context_before": map[string]any{"type": "integer"},
		"context_after":  map[string]any{"type": "integer"},
	}, "required": []string{"pattern"}}
}
func (t *GrepTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	pat, _ := args["pattern"].(string)
	sub, _ := args["path"].(string)
	gfilter, _ := args["glob"].(string)
	ctxN := intArg(args, "context", 0)
	before := intArg(args, "context_before", -1)
	after := intArg(args, "context_after", -1)
	if before < 0 {
		before = ctxN
	}
	if after < 0 {
		after = ctxN
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return tool.Result{Text: "invalid regex: " + err.Error(), IsError: true}, nil
	}
	root, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), sub)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	var lines []string
	matchCount := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if gfilter != "" {
			ok, _ := filepath.Match(gfilter, filepath.Base(p))
			if !ok {
				// also try doublestar-style on full rel? keep base Match
				return nil
			}
		}
		info, _ := d.Info()
		if info != nil && info.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		fileLines := strings.Split(string(b), "\n")
		// collect match line indices
		var hits []int
		for i, line := range fileLines {
			if re.MatchString(line) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 0 {
			return nil
		}
		// expand with context, merge overlapping ranges
		type rng struct{ lo, hi int }
		var ranges []rng
		for _, h := range hits {
			lo := h - before
			if lo < 0 {
				lo = 0
			}
			hi := h + after
			if hi >= len(fileLines) {
				hi = len(fileLines) - 1
			}
			if len(ranges) > 0 && lo <= ranges[len(ranges)-1].hi+1 {
				if hi > ranges[len(ranges)-1].hi {
					ranges[len(ranges)-1].hi = hi
				}
			} else {
				ranges = append(ranges, rng{lo, hi})
			}
		}
		for _, rg := range ranges {
			if len(lines) > 0 {
				lines = append(lines, "--")
			}
			for i := rg.lo; i <= rg.hi; i++ {
				sep := ":"
				// mark context vs match
				isHit := false
				for _, h := range hits {
					if h == i {
						isHit = true
						break
					}
				}
				if !isHit && (before > 0 || after > 0) {
					sep = "-"
				}
				lines = append(lines, fmt.Sprintf("%s%s%d%s%s", rel, sep, i+1, sep, common.TruncateRunes(fileLines[i], common.GrepLineMaxRunes)))
			}
			matchCount += 1
			if matchCount >= common.GrepMaxMatches || len(lines) >= common.GrepMaxLines {
				return fs.SkipAll
			}
		}
		return nil
	})
	if len(lines) == 0 {
		return tool.Result{Text: "(no matches)"}, nil
	}
	return tool.Result{Text: strings.Join(lines, "\n")}, nil
}
