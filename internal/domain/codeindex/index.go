// Package codeindex provides a lightweight inverted index + retriever for workspace code.
// No external embedding service; token TF scoring is enough for agent code_search tool.
package codeindex

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/vector"
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
	Files   int    `json:"files"`
	Tokens  int    `json:"tokens"`
	Root    string `json:"root"`
	BuiltAt int64  `json:"builtAt"`
}

// Index is an in-memory inverted index over workspace files.
type Index struct {
	mu   sync.RWMutex
	root string
	// term -> path -> tf
	posting map[string]map[string]int
	// path -> first lines for snippet
	docs  map[string][]string
	stats Stats
	// semantic search (optional): file summary vectors
	embed   port.IEmbeddingPort
	fileEmb map[string][]float32
	// chunk-level dense retrieval (optional, RAG). When vecIdx is set,
	// BuildEmbeddings also indexes code chunks into it (collection vecColl),
	// and SearchSemanticChunks queries them for precise code snippets.
	vecIdx  vector.IVectorIndex
	vecColl string
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
		fileEmb: map[string][]float32{},
	}
}

// SetEmbedder enables optional semantic search over file summaries.
func (idx *Index) SetEmbedder(e port.IEmbeddingPort) { idx.embed = e }

// SetVectorIndex enables chunk-level semantic retrieval backed by an
// IVectorIndex (e.g. MemIndex / Qdrant). coll namespaces the points; subsequent
// BuildEmbeddings populate it and SearchSemanticChunks query it. Safe to call
// with a nil index or empty collection (no-op).
func (idx *Index) SetVectorIndex(v vector.IVectorIndex, coll string) {
	if v != nil && coll != "" {
		idx.vecIdx = v
		idx.vecColl = coll
	}
}

// ChunkHit is a ranked chunk-level search result returned by SearchSemanticChunks.
type ChunkHit struct {
	Path       string  `json:"path"`
	ChunkIndex int     `json:"chunk_index"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet"`
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

// BuildEmbeddings computes a summary vector per file for semantic search.
// Capped by maxFiles to bound cost (files are processed in lexical order).
// Returns the number of files embedded.
func (idx *Index) BuildEmbeddings(ctx context.Context, maxFiles int) int {
	if idx == nil || idx.embed == nil {
		return 0
	}
	if maxFiles <= 0 {
		maxFiles = 300
	}
	idx.mu.RLock()
	paths := make([]string, 0, len(idx.docs))
	for p := range idx.docs {
		paths = append(paths, p)
	}
	idx.mu.RUnlock()
	sort.Strings(paths)
	if len(paths) > maxFiles {
		paths = paths[:maxFiles]
	}

	emb := map[string][]float32{}
	// batch embeddings to reduce round-trips
	const batch = 32
	for i := 0; i < len(paths); i += batch {
		end := i + batch
		if end > len(paths) {
			end = len(paths)
		}
		batchPaths := paths[i:end]
		texts := make([]string, 0, len(batchPaths))
		for _, p := range batchPaths {
			texts = append(texts, idx.fileSummary(p))
		}
		vecs, err := idx.embed.Embed(ctx, texts)
		if err != nil {
			continue
		}
		for j, p := range batchPaths {
			if j < len(vecs) && len(vecs[j]) > 0 {
				emb[p] = vecs[j]
			}
		}
	}
	idx.mu.Lock()
	idx.fileEmb = emb
	idx.mu.Unlock()

	// Sprint 2.1: chunk-level dense indexing for RAG retrieval. Degrades
	// silently if the vector backend is unavailable or embedding fails.
	if idx.vecIdx != nil && idx.embed != nil {
		if dim := idx.embed.Dims(); dim > 0 {
			idx.indexChunks(ctx, paths, dim)
		}
	}
	return len(emb)
}

// indexChunks splits each indexed file into chunks, embeds them, and upserts the
// vectors into the configured IVectorIndex collection. Errors are ignored so a
// failing remote backend never breaks the build.
func (idx *Index) indexChunks(ctx context.Context, paths []string, dim int) {
	if err := idx.vecIdx.Ensure(ctx, idx.vecColl, dim); err != nil {
		return
	}
	type pendingChunk struct {
		id      string
		snippet string
		payload map[string]any
	}
	var all []pendingChunk
	for _, p := range paths {
		full, err := os.ReadFile(filepath.Join(idx.root, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		for _, c := range chunkContent(string(full)) {
			all = append(all, pendingChunk{
				id:      p + "#" + strconv.Itoa(c.Index),
				snippet: snippetOf(c.Text, 300),
				payload: map[string]any{
					"rel_path":    p,
					"chunk_index": c.Index,
					"line_start":  c.LineStart,
				},
			})
		}
	}
	const batch = 32
	for i := 0; i < len(all); i += batch {
		end := i + batch
		if end > len(all) {
			end = len(all)
		}
		group := all[i:end]
		texts := make([]string, len(group))
		for j, g := range group {
			texts[j] = g.snippet
		}
		vecs, eerr := idx.embed.Embed(ctx, texts)
		if eerr != nil || len(vecs) != len(group) {
			continue
		}
		points := make([]vector.Point, len(group))
		for j, g := range group {
			payload := g.payload
			payload["snippet"] = g.snippet
			points[j] = vector.Point{ID: g.id, Vector: vecs[j], Payload: payload}
		}
		_ = idx.vecIdx.Upsert(ctx, idx.vecColl, points)
	}
}

// chunkContent splits source text into ~chunkTargetRunes windows (no overlap
// for simplicity) and records the 1-based starting line of each chunk.
func chunkContent(content string) []codeChunk {
	lines := strings.Split(content, "\n")
	var chunks []codeChunk
	var buf strings.Builder
	lineStart := 1
	runeCount := 0
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		chunks = append(chunks, codeChunk{Index: len(chunks), LineStart: lineStart, Text: buf.String()})
		buf.Reset()
		runeCount = 0
	}
	for i, ln := range lines {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
			runeCount++
		}
		buf.WriteString(ln)
		runeCount += utf8.RuneCountInString(ln)
		if runeCount >= chunkTargetRunes {
			flush()
			lineStart = i + 2
		}
	}
	flush()
	return chunks
}

type codeChunk struct {
	Index     int
	LineStart int
	Text      string
}

// SearchSemanticChunks returns the top-k code chunks most similar to query using
// the dense vector index (when configured). Returns nil when embedding or the
// vector index is unavailable, or no hits are found.
func (idx *Index) SearchSemanticChunks(ctx context.Context, query string, k int) []ChunkHit {
	if idx == nil || idx.embed == nil || idx.vecIdx == nil || query == "" {
		return nil
	}
	if k <= 0 {
		k = 8
	}
	qvecs, err := idx.embed.Embed(ctx, []string{query})
	if err != nil || len(qvecs) != 1 || len(qvecs[0]) == 0 {
		return nil
	}
	hits, err := idx.vecIdx.Search(ctx, idx.vecColl, qvecs[0], k, nil)
	if err != nil || len(hits) == 0 {
		return nil
	}
	out := make([]ChunkHit, 0, len(hits))
	for _, h := range hits {
		rel, _ := h.Payload["rel_path"].(string)
		ci, _ := h.Payload["chunk_index"].(float64)
		snip, _ := h.Payload["snippet"].(string)
		out = append(out, ChunkHit{
			Path:       rel,
			ChunkIndex: int(ci),
			Score:      float64(h.Score),
			Snippet:    snip,
		})
	}
	return out
}

// fileSummary builds a compact textual summary of a file for embedding.
func (idx *Index) fileSummary(path string) string {
	idx.mu.RLock()
	lines := idx.docs[path]
	idx.mu.RUnlock()
	s := path
	if len(lines) > 0 {
		joined := strings.Join(lines, " ")
		if len(joined) > 500 {
			joined = joined[:500]
		}
		s += " " + joined
	}
	return s
}

// SearchSemantic ranks files by cosine similarity of the query against file
// summaries. Returns nil when the embedder is unavailable or no vectors exist.
func (idx *Index) SearchSemantic(ctx context.Context, query string, k int) []Hit {
	if idx == nil || idx.embed == nil || query == "" {
		return nil
	}
	if k <= 0 {
		k = 8
	}
	qvecs, err := idx.embed.Embed(ctx, []string{query})
	if err != nil || len(qvecs) != 1 || len(qvecs[0]) == 0 {
		return nil
	}
	qvec := qvecs[0]

	idx.mu.RLock()
	type kv struct {
		p string
		s float64
	}
	var arr []kv
	for p, vec := range idx.fileEmb {
		if len(vec) == 0 {
			continue
		}
		if sim := cosSimIdx(qvec, vec); sim > 0.2 {
			arr = append(arr, kv{p, sim})
		}
	}
	idx.mu.RUnlock()

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
		out = append(out, Hit{Path: e.p, Score: e.s, Snippet: idx.snippetForPath(e.p)})
	}
	return out
}

func (idx *Index) snippetForPath(p string) string {
	idx.mu.RLock()
	lines := idx.docs[p]
	idx.mu.RUnlock()
	if len(lines) == 0 {
		return ""
	}
	s := strings.TrimSpace(lines[0])
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// cosSimIdx computes cosine similarity between two float32 vectors.
func cosSimIdx(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
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

// snippetOf truncates s to at most n runes, appending an ellipsis when cut.
func snippetOf(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// chunkTargetRunes is the approximate target size of a code chunk window.
const chunkTargetRunes = 1200
