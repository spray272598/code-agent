package common

// TokenCounter abstracts token estimation so the heuristic implementation can
// be swapped for a real BPE tokenizer (e.g. tiktoken-go for OpenAI-family
// models) without touching call sites. This mirrors the pluggable counters
// used by reference agents (grok-build's ItemTokenCounter seam): the budget and
// compression logic depend only on the interface, never on a concrete meter.
type TokenCounter interface {
	// CountTokens returns an approximate token count for text.
	CountTokens(text string) int
}

// HeuristicCounter is the default TokenCounter backed by the language-aware
// EstimateTokens heuristic. Replace it with a TiktokenCounter (or similar)
// to get exact counts for a specific model family.
type HeuristicCounter struct{}

// CountTokens implements TokenCounter using the EstimateTokens heuristic.
func (HeuristicCounter) CountTokens(text string) int {
	return EstimateTokens(text)
}
