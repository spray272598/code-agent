package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/observability"
)

// OpenAIEmbedding calls an OpenAI-compatible /embeddings endpoint.
// Works with providers like SiliconFlow (BAAI/bge-m3) which share the same
// api key / base URL as the chat endpoint.
type OpenAIEmbedding struct {
	apiKey  string
	apiBase string
	model   string
	client  *http.Client
}

// NewOpenAIEmbedding builds an embedding client. apiBase defaults to the
// OpenAI v1 base URL; model defaults to a common bge model.
func NewOpenAIEmbedding(apiKey, apiBase, model string) *OpenAIEmbedding {
	if apiBase == "" {
		apiBase = "https://api.siliconflow.cn/v1"
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if model == "" {
		model = "BAAI/bge-m3"
	}
	return &OpenAIEmbedding{
		apiKey: apiKey, apiBase: apiBase, model: model,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Dims is a best-effort dimension hint for bge-m3; callers treat 0 as unknown.
func (e *OpenAIEmbedding) Dims() int {
	switch {
	case strings.Contains(strings.ToLower(e.model), "bge-m3"):
		return 1024
	case strings.Contains(strings.ToLower(e.model), "bge-large"):
		return 1024
	case strings.Contains(strings.ToLower(e.model), "bge-small"):
		return 512
	default:
		return 0
	}
}

// Embed requests vectors for the given texts. Texts are batched as-is.
func (e *OpenAIEmbedding) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	observability.Current().AddEmbeddingCalls(int64(len(texts)))
	if e == nil || e.apiKey == "" {
		observability.Current().AddEmbeddingErrors(int64(len(texts)))
		return nil, fmt.Errorf("embedding: no api key")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"model":           e.model,
		"input":           texts,
		"encoding_format": "float",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiBase+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		observability.Current().AddEmbeddingErrors(int64(len(texts)))
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		observability.Current().AddEmbeddingErrors(int64(len(texts)))
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding http %d: %s", resp.StatusCode, string(b))
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		observability.Current().AddEmbeddingErrors(int64(len(texts)))
		return nil, err
	}
	vecs := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		vecs = append(vecs, d.Embedding)
	}
	if len(vecs) != len(texts) {
		observability.Current().AddEmbeddingErrors(int64(len(texts)))
		return nil, fmt.Errorf("embedding: got %d vectors for %d inputs", len(vecs), len(texts))
	}
	return vecs, nil
}
