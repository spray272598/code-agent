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
