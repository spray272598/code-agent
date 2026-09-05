package common

import (
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

// TiktokenCounter is a TokenCounter backed by tiktoken-go for exact BPE token
// counting. It supports OpenAI-family models (gpt-4, gpt-3.5-turbo, etc.) and
// falls back to the cl100k_base encoding which is the default for most modern
// models. Token counts are cached per encoding to avoid re-initialization.
type TiktokenCounter struct {
	encoding string
}

// NewTiktokenCounter creates a TiktokenCounter for the given encoding name.
// Common encodings: "cl100k_base" (gpt-4, gpt-3.5-turbo), "p50k_base" (davinci).
// If encoding is empty, defaults to "cl100k_base".
func NewTiktokenCounter(encoding string) *TiktokenCounter {
	if encoding == "" {
		encoding = "cl100k_base"
	}
	return &TiktokenCounter{encoding: encoding}
}

// CountTokens returns the exact token count using BPE encoding.
// Uses a sync.Map to cache tiktoken instances by encoding name.
func (t *TiktokenCounter) CountTokens(text string) int {
	if text == "" {
		return 0
	}
	tk, err := t.getEncoding()
	if err != nil {
		// Fallback to heuristic on error
		return EstimateTokens(text)
	}
	tokens := tk.Encode(text, nil, nil)
	if len(tokens) == 0 {
		return 1
	}
	return len(tokens)
}

var (
	encodingCache sync.Map
)

func (t *TiktokenCounter) getEncoding() (*tiktoken.Tiktoken, error) {
	if cached, ok := encodingCache.Load(t.encoding); ok {
		return cached.(*tiktoken.Tiktoken), nil
	}
	tk, err := tiktoken.GetEncoding(t.encoding)
	if err != nil {
		return nil, err
	}
	encodingCache.Store(t.encoding, tk)
	return tk, nil
}
