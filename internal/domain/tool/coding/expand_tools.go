package coding

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// --- ApplyPatch: structured unified-diff editing (preferred over full rewrites) ---

type ApplyPatchTool struct {
	ws *Workspace
}

func NewApplyPatch(ws *Workspace) *ApplyPatchTool { return &ApplyPatchTool{ws: ws} }
func (t *ApplyPatchTool) Name() string            { return "apply_patch" }
func (t *ApplyPatchTool) Description() string {
	return "Apply a unified diff to one or more files. Args: patch (string, unified diff text). " +
		"More robust than write_file for targeted edits. Paths are sandbox-checked."
}

func (t *ApplyPatchTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"patch": map[string]any{"type": "string", "description": "unified diff; supports one or more file sections"},
	}, "required": []string{"patch"}}
}

func (t *ApplyPatchTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	patch, _ := args["patch"].(string)
	if strings.TrimSpace(patch) == "" {
		return tool.Result{Text: "apply_patch: empty patch", IsError: true}, nil
	}
	files, err := parseUnifiedDiff(patch)
	if err != nil {
		return tool.Result{Text: "apply_patch: " + err.Error(), IsError: true}, nil
	}
	var applied []string
	for _, f := range files {
		abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), f.path)
		if err != nil {
			return tool.Result{Text: fmt.Sprintf("apply_patch: %s: %s", f.path, err.Error()), IsError: true}, nil
		}
		orig, rerr := os.ReadFile(abs)
		if rerr != nil && !f.newFile {
			return tool.Result{Text: fmt.Sprintf("apply_patch: %s: read: %s", f.path, rerr.Error()), IsError: true}, nil
		}
		out, aerr := applyHunks(string(orig), f.hunks, f.newFile)
		if aerr != nil {
			return tool.Result{Text: fmt.Sprintf("apply_patch: %s: %s", f.path, aerr.Error()), IsError: true}, nil
		}
		if err := os.MkdirAll(filepathDir(abs), 0o755); err != nil {
			return tool.Result{Text: err.Error(), IsError: true}, nil
		}
		if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
			return tool.Result{Text: err.Error(), IsError: true}, nil
		}
		applied = append(applied, f.path)
	}
	return tool.Result{Text: fmt.Sprintf("apply_patch: applied %d file(s): %s", len(applied), strings.Join(applied, ", "))}, nil
}

// patchFile is one section of a unified diff.
type patchFile struct {
	path    string
	newFile bool
	hunks   []hunk
}

type hunk struct {
	oldStart, oldLines, newStart, newLines int
	lines                                  []string // context/added/removed (without leading marker)
	added                                  []bool   // true if line is added (+), false if context/removed
}

var (
	reFileDiff = regexp.MustCompile(`(?m)^diff --git a/(.+?) b/(.+)$`)
	reNewFile  = regexp.MustCompile(`(?m)^new file`)
)

func parseUnifiedDiff(patch string) ([]patchFile, error) {
	scanner := bufio.NewScanner(strings.NewReader(patch))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var files []patchFile
	var cur *patchFile
	var curHunk *hunk
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git"):
			if cur != nil {
				files = append(files, *cur)
			}
			m := reFileDiff.FindStringSubmatch(line)
			p := ""
			if len(m) == 3 {
				p = m[2]
			}
			cur = &patchFile{path: p}
			curHunk = nil
		case reNewFile.MatchString(line):
			if cur != nil {
				cur.newFile = true
			}
		case strings.HasPrefix(line, "--- "):
			// ignore
		case strings.HasPrefix(line, "+++ "):
			// ignore (path already parsed)
		case strings.HasPrefix(line, "@@"):
			if cur == nil {
				return nil, fmt.Errorf("hunk before file header")
			}
			curHunk = &hunk{}
			cur.hunks = append(cur.hunks, *curHunk)
			curHunk = &cur.hunks[len(cur.hunks)-1]
			// parse ranges from @@ -a,b +c,d @@
			re := regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
			m := re.FindStringSubmatch(line)
			if len(m) == 5 {
				curHunk.oldStart, _ = atoi(m[1])
				curHunk.oldLines, _ = atoi(m[2])
				curHunk.newStart, _ = atoi(m[3])
				curHunk.newLines, _ = atoi(m[4])
			}
		default:
			if curHunk == nil {
				continue
			}
			if len(line) == 0 {
				curHunk.lines = append(curHunk.lines, "")
				curHunk.added = append(curHunk.added, false)
				continue
			}
			switch line[0] {
			case '+':
				curHunk.lines = append(curHunk.lines, line[1:])
				curHunk.added = append(curHunk.added, true)
			case '-':
				// removed lines are not emitted to output
				curHunk.lines = append(curHunk.lines, line[1:])
				curHunk.added = append(curHunk.added, false)
			default:
				curHunk.lines = append(curHunk.lines, line[1:])
				curHunk.added = append(curHunk.added, false)
			}
		}
	}
	if cur != nil {
		files = append(files, *cur)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no diff sections found")
	}
	return files, nil
}

// applyHunks applies parsed hunks to original content. It uses a tolerant
// match (whitespace-insensitive on context) so diffs survive minor drift.
func applyHunks(original string, hunks []hunk, newFile bool) (string, error) {
	if newFile {
		var b strings.Builder
		for _, h := range hunks {
			for i, l := range h.lines {
				if h.added[i] {
					b.WriteString(l)
					b.WriteString("\n")
				}
			}
		}
		return b.String(), nil
	}
	srcLines := strings.Split(original, "\n")
	out := make([]string, 0, len(srcLines))
	idx := 0
	for hi, h := range hunks {
		// consume unchanged lines up to the hunk's oldStart (1-based)
		target := h.oldStart - 1
		if target < idx {
			return "", fmt.Errorf("hunk %d: overlapping/invalid position", hi+1)
		}
		for idx < target {
			out = append(out, srcLines[idx])
			idx++
		}
		// verify context lines (tolerant)
		ci := 0
		for ; ci < len(h.lines); ci++ {
			if h.added[ci] {
				continue // added lines have no source counterpart
			}
			if idx >= len(srcLines) {
				return "", fmt.Errorf("hunk %d: context past EOF", hi+1)
			}
			if !tolerantEqual(srcLines[idx], h.lines[ci]) {
				return "", fmt.Errorf("hunk %d: context mismatch near %q", hi+1, trim40(h.lines[ci]))
			}
			idx++ // consume the (matched) source line
		}
		// emit: added lines only (context already copied via out; but we appended
		// context during the verify loop — so only emit added lines here)
		for i, l := range h.lines {
			if h.added[i] {
				out = append(out, l)
			}
		}
	}
	for idx < len(srcLines) {
		out = append(out, srcLines[idx])
		idx++
	}
	return strings.Join(out, "\n"), nil
}

func tolerantEqual(a, b string) bool {
	if a == b {
		return true
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func trim40(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not int")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func filepathDir(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "."
	}
	return p[:i]
}

// --- Lint: static analysis gate ---

type LintTool struct {
	ws *Workspace
}

func NewLint(ws *Workspace) *LintTool { return &LintTool{ws: ws} }
func (t *LintTool) Name() string      { return "lint" }
func (t *LintTool) Description() string {
	return "Run static analysis on a path or file. Args: path? (default workspace root), strict?(bool). " +
		"Prefers golangci-lint, falls back to `go vet`."
}

func (t *LintTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path":   map[string]any{"type": "string", "description": "file or dir; default workspace root"},
		"strict": map[string]any{"type": "boolean", "description": "treat warnings as errors"},
	}, "required": []string{}}
}

func (t *LintTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	root := t.ws.EffectiveRoot(tool.SessionIDFrom(ctx))
	p, _ := args["path"].(string)
	if p != "" {
		abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), p)
		if err != nil {
			return tool.Result{Text: err.Error(), IsError: true}, nil
		}
		root = abs
	}
	if exe, err := exec.LookPath("golangci-lint"); err == nil {
		out, _ := runCmd(ctx, exe, "run", "--timeout", "120s", root)
		return tool.Result{Text: out}, nil
	}
	out, _ := runCmd(ctx, "go", "vet", root)
	return tool.Result{Text: out}, nil
}

// --- Codecov: coverage gate ---

type CodecovTool struct {
	ws *Workspace
}

func NewCodecov(ws *Workspace) *CodecovTool { return &CodecovTool{ws: ws} }
func (t *CodecovTool) Name() string         { return "codecov" }
func (t *CodecovTool) Description() string {
	return "Run tests with coverage and report a summary + optional min threshold. " +
		"Args: path?(default ./...), min?(float 0-100, fail below)."
}

func (t *CodecovTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string"},
		"min":  map[string]any{"type": "number", "description": "minimum coverage percent"},
	}, "required": []string{}}
}

func (t *CodecovTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	root := t.ws.EffectiveRoot(tool.SessionIDFrom(ctx))
	p, _ := args["path"].(string)
	if p != "" {
		abs, err := t.ws.ResolveForSession(tool.SessionIDFrom(ctx), p)
		if err != nil {
			return tool.Result{Text: err.Error(), IsError: true}, nil
		}
		root = abs
	}
	runCmd(ctx, "go", "test", "-coverprofile=coverage.out", root)
	out, _ := runCmd(ctx, "go", "tool", "cover", "-func=coverage.out")
	// parse total from last line "total: ..."
	total := parseTotalCoverage(out)
	min, hasMin := args["min"].(float64)
	if hasMin && total < min {
		return tool.Result{Text: fmt.Sprintf("codecov: %.1f%% < min %.1f%%\n%s", total, min, out), IsError: true}, nil
	}
	return tool.Result{Text: fmt.Sprintf("codecov: total %.1f%%\n%s", total, out)}, nil
}

func parseTotalCoverage(out string) float64 {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "total:") {
			if idx := strings.Index(lines[i], "\t"); idx >= 0 {
				s := strings.TrimSuffix(strings.TrimSpace(lines[i][idx+1:]), "%")
				var f float64
				fmt.Sscanf(s, "%f", &f)
				return f
			}
		}
	}
	return 0
}

// --- WebSearch: real web tool (pluggable provider) ---

// WebSearcher abstracts a search backend so tests can stub it.
type WebSearcher interface {
	Search(ctx context.Context, query string, max int) ([]WebResult, error)
}

// WebResult is a single search hit.
type WebResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// HTTPWebSearcher queries a search endpoint (DuckDuckGo HTML by default, no
// key required; set Endpoint/Key for a paid provider).
type HTTPWebSearcher struct {
	Endpoint string
	Key      string
	Client   *http.Client
}

func (s *HTTPWebSearcher) Search(ctx context.Context, query string, max int) ([]WebResult, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = "https://html.duckduckgo.com/html/?q=" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "code-agent/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseDuckDuckGo(resp.Body, max), nil
}

type WebSearchTool struct {
	ws       *Workspace
	searcher WebSearcher
}

func NewWebSearch(ws *Workspace, searcher WebSearcher) *WebSearchTool {
	if searcher == nil {
		searcher = &HTTPWebSearcher{}
	}
	return &WebSearchTool{ws: ws, searcher: searcher}
}
func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web for current info (docs, APIs, errors). Args: query, max?(int, default 5)"
}

func (t *WebSearchTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string"},
		"max":   map[string]any{"type": "integer"},
	}, "required": []string{"query"}}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	q, _ := args["query"].(string)
	if strings.TrimSpace(q) == "" {
		return tool.Result{Text: "web_search: empty query", IsError: true}, nil
	}
	max := intArg(args, "max", 5)
	if max <= 0 || max > 20 {
		max = 5
	}
	results, err := t.searcher.Search(ctx, q, max)
	if err != nil {
		return tool.Result{Text: "web_search error: " + err.Error(), IsError: true}, nil
	}
	if len(results) == 0 {
		return tool.Result{Text: "web_search: no results"}, nil
	}
	var b strings.Builder
	for i, r := range results {
		if i >= max {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return tool.Result{Text: b.String()}, nil
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// parseDuckDuckGo extracts result links/titles from the HTML response.
func parseDuckDuckGo(r io.Reader, max int) []WebResult {
	var results []WebResult
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	reTitle := regexp.MustCompile(`class="result__a"[^>]*>(.*?)</a>`)
	reURL := regexp.MustCompile(`class="result__a"[^>]*href="([^"]+)"`)
	reSnip := regexp.MustCompile(`class="result__snippet"[^>]*>(.*?)</a>`)
	var cur WebResult
	flush := func() {
		if cur.URL != "" || cur.Title != "" {
			results = append(results, cur)
		}
		cur = WebResult{}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if m := reURL.FindStringSubmatch(line); m != nil && cur.URL == "" {
			cur.URL = decodeDDGURL(m[1])
		}
		if m := reTitle.FindStringSubmatch(line); m != nil && cur.Title == "" {
			cur.Title = stripTags(m[1])
		}
		if m := reSnip.FindStringSubmatch(line); m != nil && cur.Snippet == "" {
			cur.Snippet = stripTags(m[1])
		}
		if len(results) >= max && max > 0 {
			break
		}
		flush()
	}
	flush()
	if len(results) > max && max > 0 {
		results = results[:max]
	}
	return results
}

func decodeDDGURL(u string) string {
	// DuckDuckGo wraps redirects in uddg= param; if present decode it.
	if i := strings.Index(u, "uddg="); i >= 0 {
		rest := u[i+5:]
		if j := strings.Index(rest, "&"); j >= 0 {
			rest = rest[:j]
		}
		if dec, err := url.QueryUnescape(rest); err == nil {
			return dec
		}
	}
	return u
}

func stripTags(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	s = re.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.TrimSpace(s)
}
