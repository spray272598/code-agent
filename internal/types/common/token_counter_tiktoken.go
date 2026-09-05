package common

import (
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

// maxEncodingCacheSize limits the number of tiktoken encodings held in memory.
// Each encoding is ~10-50MB depending on vocabulary size, so 4 is a generous
// ceiling for a single-process agent (cl100k_base + p50k_base + r50k_base + 1).
const maxEncodingCacheSize = 4

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
// Uses a bounded cache to avoid re-initialization.
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

// --- bounded encoding cache ---

var (
	globalCache   = newEncodingCache()
)

type encodingEntry struct {
	encoding string
	tk       *tiktoken.Tiktoken
}

type encodingCache struct {
	mu      sync.Mutex
	entries []encodingEntry // ordered by access (newest at end)
}

func newEncodingCache() *encodingCache {
	return &encodingCache{}
}

// get returns a cached tiktoken for the given encoding, or loads + caches it.
// When the cache exceeds maxEncodingCacheSize, the oldest entry is evicted.
func (c *encodingCache) get(encoding string) (*tiktoken.Tiktoken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Hit? Move to end (most-recently-used).
	for i, e := range c.entries {
		if e.encoding == encoding {
			if i < len(c.entries)-1 {
				c.entries = append(c.entries[:i], c.entries[i+1:]...)
				c.entries = append(c.entries, encodingEntry{encoding, e.tk})
			}
			return e.tk, nil
		}
	}

	// Miss — load from tiktoken-go.
	tk, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, err
	}

	// Evict oldest if at capacity.
	if len(c.entries) >= maxEncodingCacheSize {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, encodingEntry{encoding, tk})
	return tk, nil
}

// Invalidate removes a specific encoding (or all encodings if encoding is "")
// from the cache. Safe to call concurrently.
func (c *encodingCache) Invalidate(encoding string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if encoding == "" {
		c.entries = c.entries[:0]
		return
	}
	for i, e := range c.entries {
		if e.encoding == encoding {
			c.entries = append(c.entries[:i], c.entries[i+1:]...)
			return
		}
	}
}

// InvalidateEncoding removes a specific encoding (or all if empty) from the
// global cache. Callable from tests or runtime config changes.
func InvalidateEncoding(encoding string) {
	globalCache.Invalidate(encoding)
}

func (t *TiktokenCounter) getEncoding() (*tiktoken.Tiktoken, error) {
	return globalCache.get(t.encoding)
}
