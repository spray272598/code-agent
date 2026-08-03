// Package codeindex provides a lightweight inverted index + retriever for workspace code.
// No external embedding service; token TF scoring is enough for agent code_search tool.
package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	tokenRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{1,48}|[\p{Han}]{1,12}`)
	extOK   = map[string]bool{
		".go": true, ".md": true, ".txt": true, ".yaml": true, ".yml": true,
		".json": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".py": true, ".rs": true, ".java": true, ".toml": true, ".sql": true,
		".css": true, ".html": true, ".sh": true, ".ps1": true,
	}
	skipDir = map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "bin": true,
		"dist": true, "data": true, ".idea": true, ".vscode": true,
		"coverage": true, "tmp": true, "temp": true, "reports": true,
	}
)

// Hit is a ranked search result.
type Hit struct {
	Path    string  `json:"path"`
	Score   float64 `json:"score"`
	Line    int     `json:"line,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
}

// Stats about the last build.
type Stats struct {
	Files   int `json:"files"`
	Tokens  int `json:"tokens"`
	Root    string `json:"root"`
	BuiltAt int64  `json:"builtAt"`
}

// Index is an in-memory inverted index over workspace files.
type Index struct {
	mu      sync.RWMutex
	root    string
	// term -> path -> tf
	posting map[string]map[string]int
	// path -> first lines for snippet
	docs    map[string][]string
	stats   Stats
}

func New(root string) *Index {
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	return &Index{
		root:    root,
		posting: map[string]map[string]int{},
		docs:    map[string][]string{},
	}
}

func (idx *Index) Root() string {
	if idx == nil {
		return ""
	}
	return idx.root
}

func (idx *Index) Stats() Stats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.stats
}

// Build walks workspace and rebuilds the index.
func (idx *Index) Build(_ context.Context) (Stats, error) {
	posting := map[string]map[string]int{}
	docs := map[string][]string{}
	files, tokens := 0, 0

	err := filepath.WalkDir(idx.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDir[name] || strings.HasPrefix(name, ".") && name != "." {
				if name == ".git" || skipDir[name] {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !extOK[ext] {
			return nil
		}
		// size guard
		info, err := d.Info()
		if err != nil || info.Size() > 512*1024 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// skip binary-ish
		if looksBinary(b) {
			return nil
		}
		rel, err := filepath.Rel(idx.root, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		lines := strings.Split(string(b), "\n")
		// keep first 40 lines for snippet fallback
		keep := lines
		if len(keep) > 40 {
			keep = keep[:40]
		}
		docs[rel] = keep
		tf := map[string]int{}
		for _, line := range lines {
			for _, tok := range tokenize(line) {
				tf[tok]++
				tokens++
			}
		}
		for tok, c := range tf {
			if posting[tok] == nil {
				posting[tok] = map[string]int{}
			}
			posting[tok][rel] = c
		}
		files++
		return nil
	})
	if err != nil {
		return Stats{}, err
	}

	idx.mu.Lock()
	idx.posting = posting
	idx.docs = docs
	idx.stats = Stats{Files: files, Tokens: tokens, Root: idx.root, BuiltAt: nowMs()}
	st := idx.stats
	idx.mu.Unlock()
	return st, nil
}

// Search returns top-k paths matching query tokens (sum of TF, boosted by query term hits).
func (idx *Index) Search(query string, k int) []Hit {
	if k <= 0 {
		k = 8
	}
	qtoks := tokenize(query)
	if len(qtoks) == 0 {
		return nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	score := map[string]float64{}
	for _, t := range qtoks {
		pm := idx.posting[t]
		for path, tf := range pm {
			score[path] += float64(tf)
			// slight boost for multi-term co-occurrence
			score[path] += 0.25
		}
	}
	type kv struct {
		p string
		s float64
	}
	var arr []kv
	for p, s := range score {
		arr = append(arr, kv{p, s})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].s == arr[j].s {
			return arr[i].p < arr[j].p
		}
		return arr[i].s > arr[j].s
	})
	if len(arr) > k {
		arr = arr[:k]
	}
	out := make([]Hit, 0, len(arr))
	for _, e := range arr {
		h := Hit{Path: e.p, Score: e.s, Snippet: snippetFor(idx.docs[e.p], qtoks)}
		out = append(out, h)
	}
	return out
}

func snippetFor(lines []string, qtoks []string) string {
	if len(lines) == 0 {
		return ""
	}
	// pick best matching line
	bestI, bestN := 0, -1
	for i, ln := range lines {
		low := strings.ToLower(ln)
		n := 0
		for _, t := range qtoks {
			if strings.Contains(low, t) {
				n++
			}
		}
		if n > bestN {
			bestN, bestI = n, i
		}
	}
	s := strings.TrimSpace(lines[bestI])
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func tokenize(s string) []string {
	raw := tokenRE.FindAllString(s, -1)
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, t := range raw {
		t = strings.ToLower(t)
		if len(t) < 2 || isStop(t) {
			continue
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func isStop(t string) bool {
	switch t {
	case "the", "and", "for", "with", "this", "that", "from", "into", "func", "return",
		"var", "const", "type", "package", "import", "if", "else", "true", "false", "nil",
		"string", "int", "error", "ctx", "err", "http", "json":
		return true
	}
	return false
}

func looksBinary(b []byte) bool {
	n := len(b)
	if n > 800 {
		n = 800
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
		if b[i] < 9 && !unicode.IsSpace(rune(b[i])) {
			return true
		}
	}
	return false
}

func nowMs() int64 { return time.Now().UnixMilli() }
