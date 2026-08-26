package contextx

import (
	"context"
	"fmt"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/types/common"
)

// Summarizer builds a rolling session summary from middle history (L3 compress).
type Summarizer struct {
	LLM port.ILLMPort
}

func NewSummarizer(llm port.ILLMPort) *Summarizer {
	return &Summarizer{LLM: llm}
}

// Summarize turns dropped/middle messages into one assistant-visible summary string.
func (s *Summarizer) Summarize(ctx context.Context, priorSummary string, middle []map[string]any) (string, error) {
	if s == nil || s.LLM == nil || len(middle) == 0 {
		return priorSummary, nil
	}
	var b strings.Builder
	if priorSummary != "" {
		b.WriteString("Previous summary:\n")
		b.WriteString(priorSummary)
		b.WriteString("\n\n")
	}
	b.WriteString("Conversation to compress:\n")
	for _, m := range middle {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if content == "" {
			continue
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(common.TruncateRunes(content, common.SummarizeInputMaxRunes))
		b.WriteString("\n")
	}
	resp, err := s.LLM.Generate(ctx, &port.ChatRequest{
		SystemPrompt: `You compress coding-agent chat history.
Output a short bullet summary preserving: user goals, decisions, file paths touched, errors, and next steps.
No tool JSON. Max ~250 words.`,
		Messages:    []port.ChatMessage{{Role: "user", Content: b.String()}},
		Temperature: 0.1,
		MaxTokens:   500,
	})
	if err != nil {
		// rule fallback
		return ruleSummary(priorSummary, middle), nil
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" {
		return ruleSummary(priorSummary, middle), nil
	}
	return out, nil
}

// SummarizeSingle produces a concise semantic summary for a single long message.
// Used by L0 compression instead of raw truncation. Returns the summary plus
// an "[SUMMARIZED]" marker so the LLM knows it's a compressed representation.
func (s *Summarizer) SummarizeSingle(ctx context.Context, content string, maxRunes int) (string, error) {
	if s == nil || s.LLM == nil {
		return "", fmt.Errorf("summarizer not configured")
	}
	if maxRunes <= 0 {
		maxRunes = 400
	}

	resp, err := s.LLM.Generate(ctx, &port.ChatRequest{
		SystemPrompt: `You summarize a single long message for context compression.
Produce a concise summary (~3-5 sentences) preserving: key information, decisions, errors, file paths, and conclusions.
Focus on facts, not style. No markdown.`,
		Messages:    []port.ChatMessage{{Role: "user", Content: content}},
		Temperature: 0.1,
		MaxTokens:   150,
	})
	if err != nil || strings.TrimSpace(resp.Content) == "" {
		return "", err
	}

	out := strings.TrimSpace(resp.Content)
	// Truncate if still too long
	if len([]rune(out)) > maxRunes {
		out = common.TruncateRunesKeepTail(out, maxRunes)
	}
	return "[SUMMARIZED] " + out, nil
}

// RuleSummarizeSingle is a deterministic fallback when LLM is unavailable.
// Extracts key sentences by looking for signal words.
func RuleSummarizeSingle(content string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 400
	}

	sentences := splitSentences(content)
	if len(sentences) <= 3 {
		return content
	}

	// Score sentences: those with signal words or code markers
	scores := make([]int, len(sentences))
	for i, s := range sentences {
		scores[i] = scoreSentence(s)
	}

	// Select top-scoring sentences + first + last
	selected := map[int]bool{}
	selected[0] = true
	selected[len(sentences)-1] = true

	// Take top 3 by score
	type idxScore struct{ idx, score int }
	var ranked []idxScore
	for i := 1; i < len(sentences)-1; i++ {
		ranked = append(ranked, idxScore{i, scores[i]})
	}
	for i := 0; i < len(ranked)-1; i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	for i := 0; i < 3 && i < len(ranked); i++ {
		selected[ranked[i].idx] = true
	}

	var b strings.Builder
	for i, s := range sentences {
		if selected[i] {
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString(s)
		}
	}

	result := b.String()
	if len([]rune(result)) > maxRunes {
		result = common.TruncateRunesKeepTail(result, maxRunes)
	}
	return "[RULE_SUMMARY] " + result
}

func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		current.WriteRune(runes[i])
		if runes[i] == '.' || runes[i] == '!' || runes[i] == '?' ||
			runes[i] == '\n' && i+1 < len(runes) && runes[i+1] == '\n' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}
	if current.Len() > 0 {
		s := strings.TrimSpace(current.String())
		if s != "" {
			sentences = append(sentences, s)
		}
	}
	if len(sentences) == 0 {
		return []string{text}
	}
	return sentences
}

func scoreSentence(s string) int {
	score := 0
	lower := strings.ToLower(s)
	signals := []string{
		"error", "failed", "success", "result", "结论", "结果", "失败", "成功", "错误",
		"package ", "func ", "type ", "import ", "const ", "var ",
		"DENIED", "CONFIRM", "completed", "approved", "=", "return", "nil",
	}
	for _, sig := range signals {
		if strings.Contains(lower, strings.ToLower(sig)) {
			score += 3
		}
	}
	if len(s) > 100 {
		score++
	}
	if len(s) > 200 {
		score++
	}
	return score
}

func ruleSummary(prior string, middle []map[string]any) string {
	var parts []string
	if prior != "" {
		parts = append(parts, prior)
	}
	for _, m := range middle {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if role == "user" && content != "" {
			parts = append(parts, "User: "+common.TruncateRunes(content, common.SummarizeUserMaxRunes))
		}
		if role == "tool" {
			name, _ := m["toolName"].(string)
			parts = append(parts, fmt.Sprintf("Tool %s: %s", name, common.TruncateRunes(content, common.SummarizeToolMaxRunes)))
		}
	}
	if len(parts) > 12 {
		parts = parts[len(parts)-12:]
	}
	return strings.Join(parts, "\n")
}
