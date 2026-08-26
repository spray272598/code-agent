package subagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
)

// SummarizerPromptTemplate is the default goal summarizer prompt.
const SummarizerPromptTemplate = `You are the Goal Summarizer. The goal has been achieved and verified.
Produce a concise, user-facing summary of what was accomplished.

Focus on:
- Key deliverables and changes made
- Tests passed and verification performed
- Any notable implementation details
- Next steps or follow-up recommendations

Write in clear, concise markdown. Keep the summary under 200 words.
The summary will be shown to the user as the final output of the goal.`

// GoalSummarizerOutcome is the result of a summarizer attempt.
type GoalSummarizerOutcome struct {
	Summarized bool
	Summary    string
	FailReason string
	LatencyMs  int64
}

// GoalSummarizerFailReason identifies why the summarizer failed (fail-open).
type GoalSummarizerFailReason int

const (
	SummarizerFailTransport GoalSummarizerFailReason = iota
	SummarizerFailRuntime
	SummarizerFailAborted
	SummarizerFailEmptySummary
)

// String returns the wire-compatible string for the fail reason.
func (r GoalSummarizerFailReason) String() string {
	switch r {
	case SummarizerFailTransport:
		return "transport"
	case SummarizerFailRuntime:
		return "runtime"
	case SummarizerFailAborted:
		return "aborted"
	case SummarizerFailEmptySummary:
		return "empty_summary"
	default:
		return "unknown"
	}
}

// SummarizerInput holds the inputs for running the goal summarizer.
type SummarizerInput struct {
	Objective    string
	Plan         *GoalPlan
	WorkOutput   string
	Attempt      int
	ModelID      string
	MaxChars     int
}

// GoalSummarizer is the interface for post-completion summarization.
type GoalSummarizer interface {
	Summarize(ctx context.Context, input SummarizerInput) (*GoalSummarizerOutcome, error)
}

// LLMSummarizer implements GoalSummarizer using an LLM.
type LLMSummarizer struct {
	LLM    port.ILLMPort
	Model  string
	MaxTokens int
}

// NewLLMSummarizer creates a new LLM-based goal summarizer.
func NewLLMSummarizer(llm port.ILLMPort) *LLMSummarizer {
	return &LLMSummarizer{
		LLM:       llm,
		MaxTokens: 512,
	}
}

// Summarize runs the goal summarizer and returns the outcome.
func (s *LLMSummarizer) Summarize(ctx context.Context, input SummarizerInput) (*GoalSummarizerOutcome, error) {
	if s.LLM == nil {
		return &GoalSummarizerOutcome{
			Summarized: false,
			FailReason: SummarizerFailRuntime.String(),
		}, nil
	}

	if input.MaxChars <= 0 {
		input.MaxChars = 1200
	}

	started := time.Now()
	prompt := buildSummarizerPrompt(input)

	resp, err := s.LLM.Generate(ctx, &port.ChatRequest{
		SystemPrompt: SummarizerPromptTemplate,
		Messages:     []port.ChatMessage{{Role: "user", Content: prompt}},
		Temperature:  0.3,
		MaxTokens:    s.MaxTokens,
	})

	latencyMs := time.Since(started).Milliseconds()

	if err != nil {
		failReason := SummarizerFailRuntime
		if ctx.Err() != nil {
			failReason = SummarizerFailAborted
		}
		return &GoalSummarizerOutcome{
			Summarized: false,
			FailReason: failReason.String(),
			LatencyMs:  latencyMs,
		}, nil
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return &GoalSummarizerOutcome{
			Summarized: false,
			FailReason: SummarizerFailEmptySummary.String(),
			LatencyMs:  latencyMs,
		}, nil
	}

	// Truncate if exceeds max chars
	maxChars := input.MaxChars
	if len([]rune(summary)) > maxChars {
		runes := []rune(summary)
		summary = string(runes[:maxChars]) + " ..."
	}

	return &GoalSummarizerOutcome{
		Summarized: true,
		Summary:    summary,
		LatencyMs:  latencyMs,
	}, nil
}

// buildSummarizerPrompt constructs the user prompt for the summarizer.
func buildSummarizerPrompt(input SummarizerInput) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Objective\n%s\n\n", input.Objective))

	if input.Plan != nil {
		b.WriteString("## Plan\n")
		b.WriteString(fmt.Sprintf("- Kind: %s\n", input.Plan.Kind.String()))
		if len(input.Plan.AcceptanceCriteria) > 0 {
			b.WriteString("- Acceptance criteria:\n")
			for _, c := range input.Plan.AcceptanceCriteria {
				b.WriteString(fmt.Sprintf("  - %s\n", c))
			}
		}
		if len(input.Plan.VerificationPlan) > 0 {
			b.WriteString("- Verification performed:\n")
			for _, v := range input.Plan.VerificationPlan {
				b.WriteString(fmt.Sprintf("  - %s\n", v))
			}
		}
		b.WriteString("\n")
	}

	if input.WorkOutput != "" {
		b.WriteString("## Work Output\n")
		if len(input.WorkOutput) > 4000 {
			b.WriteString(input.WorkOutput[:4000])
			b.WriteString("\n... [truncated]")
		} else {
			b.WriteString(input.WorkOutput)
		}
		b.WriteString("\n\n")
	}

	b.WriteString("Provide a concise summary of what was accomplished.")
	return b.String()
}

// SimpleSummarizer implements a basic rule-based summarizer.
type SimpleSummarizer struct{}

// Summarize implements a simple rule-based summary extraction.
func (s *SimpleSummarizer) Summarize(_ context.Context, input SummarizerInput) (*GoalSummarizerOutcome, error) {
	latencyMs := int64(10)
	summary := generateSimpleSummary(input)

	if summary == "" {
		return &GoalSummarizerOutcome{
			Summarized: false,
			FailReason: SummarizerFailEmptySummary.String(),
			LatencyMs:  latencyMs,
		}, nil
	}

	maxChars := input.MaxChars
	if maxChars <= 0 {
		maxChars = 1200
	}
	if len([]rune(summary)) > maxChars {
		runes := []rune(summary)
		summary = string(runes[:maxChars]) + " ..."
	}

	return &GoalSummarizerOutcome{
		Summarized: true,
		Summary:    summary,
		LatencyMs:  latencyMs,
	}, nil
}

// generateSimpleSummary creates a basic summary from the input.
func generateSimpleSummary(input SummarizerInput) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Summary: %s\n\n", input.Objective))

	if input.Plan != nil {
		b.WriteString("### What was accomplished:\n")
		if len(input.Plan.TaskChecklist) > 0 {
			for _, task := range input.Plan.TaskChecklist {
				b.WriteString(fmt.Sprintf("- %s\n", task))
			}
		} else {
			b.WriteString("- Goal completed successfully\n")
		}
		b.WriteString("\n")

		if len(input.Plan.AcceptanceCriteria) > 0 {
			b.WriteString("### Acceptance criteria met:\n")
			for _, c := range input.Plan.AcceptanceCriteria {
				b.WriteString(fmt.Sprintf("- ✅ %s\n", c))
			}
		}
	}

	b.WriteString("\n### Status: Goal achieved and verified.")
	return b.String()
}

// RunSummarizerWithFallback tries LLM summarizer first, falls back to simple.
func RunSummarizerWithFallback(ctx context.Context, llm port.ILLMPort, input SummarizerInput) (*GoalSummarizerOutcome, error) {
	// Try LLM summarizer
	if llm != nil {
		summarizer := NewLLMSummarizer(llm)
		outcome, err := summarizer.Summarize(ctx, input)
		if err != nil || !outcome.Summarized {
			// Fall back to simple summarizer
			simple := &SimpleSummarizer{}
			return simple.Summarize(ctx, input)
		}
		return outcome, err
	}

	// No LLM available, use simple summarizer
	simple := &SimpleSummarizer{}
	return simple.Summarize(ctx, input)
}
