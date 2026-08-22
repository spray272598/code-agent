package codeindex

import (
	"context"
	"fmt"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// SearchTool agent tool: code_search
type SearchTool struct {
	Idx *Index
}

func NewSearchTool(idx *Index) *SearchTool { return &SearchTool{Idx: idx} }

func (t *SearchTool) Name() string { return "code_search" }
func (t *SearchTool) Description() string {
	return "Search the workspace code index (inverted index). Args: query (required), top_k? (default 8). Prefer this over blind glob when looking for symbols/files."
}
func (t *SearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"top_k": map[string]any{"type": "integer"},
		},
		"required": []string{"query"},
	}
}
func (t *SearchTool) Execute(_ context.Context, args map[string]any) (tool.Result, error) {
	if t.Idx == nil {
		return tool.Result{Text: "code index unavailable", IsError: true}, nil
	}
	q, _ := args["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return tool.Result{Text: "query required", IsError: true}, nil
	}
	k := 8
	switch v := args["top_k"].(type) {
	case float64:
		k = int(v)
	case int:
		k = v
	}
	st := t.Idx.Stats()
	if st.Files == 0 {
		if _, err := t.Idx.Build(context.Background()); err != nil {
			return tool.Result{Text: "index build failed: " + err.Error(), IsError: true}, nil
		}
	}
	hits := t.Idx.Search(q, k)
	if len(hits) == 0 {
		// semantic fallback: natural-language query with no literal token hits.
		// Prefer chunk-level RAG hits (precise code snippets) when available.
		if chunks := t.Idx.SearchSemanticChunks(context.Background(), q, k); len(chunks) > 0 {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("code_search %q → %d semantic chunk hit(s) (indexed files=%d)\n", q, len(chunks), t.Idx.Stats().Files))
			for i, c := range chunks {
				b.WriteString(fmt.Sprintf("%d. %s#L%d score=%.2f\n   %s\n", i+1, c.Path, c.ChunkIndex, c.Score, c.Snippet))
			}
			return tool.Result{Text: b.String()}, nil
		}
		if sem := t.Idx.SearchSemantic(context.Background(), q, k); len(sem) > 0 {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("code_search %q → %d semantic file hit(s) (indexed files=%d)\n", q, len(sem), t.Idx.Stats().Files))
			for i, h := range sem {
				b.WriteString(fmt.Sprintf("%d. %s score=%.2f\n   %s\n", i+1, h.Path, h.Score, h.Snippet))
			}
			return tool.Result{Text: b.String()}, nil
		}
		return tool.Result{Text: fmt.Sprintf("no hits for %q (indexed files=%d)", q, t.Idx.Stats().Files)}, nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("code_search %q → %d hits (files=%d)\n", q, len(hits), t.Idx.Stats().Files))
	for i, h := range hits {
		b.WriteString(fmt.Sprintf("%d. %s score=%.2f\n   %s\n", i+1, h.Path, h.Score, h.Snippet))
	}
	return tool.Result{Text: b.String()}, nil
}

// RebuildTool agent tool: code_index
type RebuildTool struct {
	Idx *Index
}

func NewRebuildTool(idx *Index) *RebuildTool { return &RebuildTool{Idx: idx} }

func (t *RebuildTool) Name() string { return "code_index" }
func (t *RebuildTool) Description() string {
	return "Rebuild the workspace code index. Args: none required. Call after many file edits."
}
func (t *RebuildTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *RebuildTool) Execute(ctx context.Context, _ map[string]any) (tool.Result, error) {
	if t.Idx == nil {
		return tool.Result{Text: "code index unavailable", IsError: true}, nil
	}
	st, err := t.Idx.Build(ctx)
	if err != nil {
		return tool.Result{Text: err.Error(), IsError: true}, nil
	}
	return tool.Result{Text: fmt.Sprintf("indexed files=%d tokens=%d root=%s", st.Files, st.Tokens, st.Root)}, nil
}
