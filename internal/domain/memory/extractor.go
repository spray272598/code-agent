package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// ExtractedItem is a structured memory unit extracted from user input.
type ExtractedItem struct {
	Category   string `json:"category"`
	Content    string `json:"content"`
	Importance int    `json:"importance"`
}

// Extractor decides whether user input contains a durable preference/fact/correction
// and extracts it structurally. Implementations may be rule-based or LLM-based.
type Extractor interface {
	Extract(ctx context.Context, text string) ([]ExtractedItem, error)
}

// LLMExtractor uses an LLM to semantically judge and structurally extract memory,
// replacing the previous keyword-trigger + raw-text approach.
type LLMExtractor struct {
	llm     port.ILLMPort
	timeout time.Duration
}

// NewLLMExtractor builds an LLM-backed extractor. A nil llm is allowed and will
// make Extract return an error so callers fall back to rules.
func NewLLMExtractor(llm port.ILLMPort) *LLMExtractor {
	return &LLMExtractor{llm: llm, timeout: 3 * time.Second}
}

const extractSystemPrompt = `You extract durable facts and preferences from a single user message for a long-term memory system.

Rules:
- Extract ONLY if the message states a stable preference, fact, decision, or correction about how the user wants future work done (e.g. "以后用英文注释", "prefer go test", "remember my username is bob").
- IGNORE one-off requests, questions, greetings, code snippets, and transient chatter.
- Write each memory as a concise, self-contained sentence in the user's language.
- Output STRICT JSON array only. Each element: {"category":"preference|fact|correction|identity|other","content":"...","importance":0-100}.
- If nothing is worth remembering, output [].`

// Extract calls the LLM with a short timeout and parses the JSON array result.
func (e *LLMExtractor) Extract(ctx context.Context, text string) ([]ExtractedItem, error) {
	if e == nil || e.llm == nil {
		return nil, fmt.Errorf("memory extractor: no LLM")
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	resp, err := e.llm.Generate(ctx, &port.ChatRequest{
		SystemPrompt: extractSystemPrompt,
		Messages:     []port.ChatMessage{{Role: "user", Content: text}},
		Temperature:  0.1,
		MaxTokens:    300,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("memory extractor: empty response")
	}
	return parseExtracted(resp.Content)
}

// parseExtracted extracts the first JSON array from a possibly noisy LLM reply.
func parseExtracted(content string) ([]ExtractedItem, error) {
	content = strings.TrimSpace(content)
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("memory extractor: no JSON array in %q", truncateRunes(content, 120))
	}
	var items []ExtractedItem
	if err := json.Unmarshal([]byte(content[start:end+1]), &items); err != nil {
		return nil, fmt.Errorf("memory extractor: decode: %w", err)
	}
	out := make([]ExtractedItem, 0, len(items))
	for _, it := range items {
		it.Content = strings.TrimSpace(it.Content)
		if it.Content == "" {
			continue
		}
		if it.Category == "" {
			it.Category = "general"
		}
		out = append(out, it)
	}
	return out, nil
}
