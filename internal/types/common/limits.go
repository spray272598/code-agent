package common

// Centralized truncation / count limits for tool outputs, context compression
// and search. Tuning happens here instead of scattering magic numbers across
// packages. Keep names self-describing so call sites read clearly.

// Rune-based truncation limits.
const (
	// Tool output truncation
	BashOutputMaxRunes         = 6000 // bash tool stdout+stderr cap
	ReadFileMaxRunes           = 8000 // read_file tool content cap
	SubAgentToolResultMaxRunes = 3000 // subagent per-tool result cap
	GrepLineMaxRunes           = 200  // grep single-line snippet cap

	// Context compression truncation (contextx)
	CompressLongContentMaxRunes = 2000 // L0 per-message content cap
	SummarizeInputMaxRunes      = 400  // L3 summarizer per-message input cap
	SummarizeUserMaxRunes       = 120  // rule-summary user message cap
	SummarizeToolMaxRunes       = 80   // rule-summary tool message cap

	// Search snippet
	CodeSnippetMaxRunes = 160 // codeindex best-line snippet cap
)

// Count limits for file / content search tools.
const (
	GlobMaxMatches = 200 // glob tool max matched files
	GrepMaxMatches = 100 // grep tool max matched files
	GrepMaxLines   = 400 // grep tool max emitted lines
)
