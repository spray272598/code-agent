package port

import "context"

// IEmbeddingPort turns text into dense vectors for semantic search.
// Implemented in infrastructure/llm (OpenAI-compatible /embeddings endpoint).
type IEmbeddingPort interface {
	// Embed returns one vector per input text, same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dims returns the vector dimension, or 0 when unknown/unavailable.
	Dims() int
}
