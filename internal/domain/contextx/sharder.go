package contextx

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ShardSegment is a logical segment extracted from a long message.
type ShardSegment struct {
	Text      string
	Index     int
	Important bool // contains error/result/conclusion markers
}

// ShardConfig controls sharding behavior.
type ShardConfig struct {
	MaxSegments  int // max segments to keep (default 6)
	HeadSegments int // always keep N head segments (default 2)
	TailSegments int // always keep N tail segments (default 2)
	MaxRunes     int // total rune budget for output (default 2000)
}

func DefaultShardConfig() ShardConfig {
	return ShardConfig{
		MaxSegments:  6,
		HeadSegments: 2,
		TailSegments: 2,
		MaxRunes:     2000,
	}
}

// importantPatterns marks segments that likely contain key information.
var importantPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)error|failed|success|result|结论|结果|失败|成功|错误`),
	regexp.MustCompile(`(?i)^\s*(package|func|type|import|const|var)\s`),
	regexp.MustCompile(`^\s*(==|!=|<=|>=|<|>)\s`),
	regexp.MustCompile(`(?i)(DENIED|CONFIRM|required|completed|approved)`),
}

// ShardLongText splits a long text into logical segments and keeps the most
// important ones within the budget. Returns the sharded text plus a marker
// indicating how much was omitted.
//
// Strategy:
//  1. Split by paragraph boundaries (\n\n) first; fallback to single \n
//  2. Score each segment (important patterns > generic)
//  3. Always keep head + tail segments
//  4. Fill remaining budget with highest-scoring middle segments
//  5. If still over budget, truncate the lowest-value segments
func ShardLongText(text string, cfg ShardConfig) string {
	if cfg.MaxRunes <= 0 {
		cfg = DefaultShardConfig()
	}
	runes := []rune(text)
	if len(runes) <= cfg.MaxRunes {
		return text
	}

	segments := splitIntoSegments(text)
	if len(segments) == 0 {
		return text
	}

	// Mark important segments
	for i := range segments {
		segments[i].Important = isImportant(segments[i].Text)
		segments[i].Index = i
	}

	// Score each segment: important gets +10, longer gets +1 per 100 runes
	scores := make([]int, len(segments))
	for i, seg := range segments {
		score := 0
		if seg.Important {
			score += 10
		}
		runeLen := utf8.RuneCountInString(seg.Text)
		score += runeLen / 100
		scores[i] = score
	}

	// Select segments to keep
	keepSet := selectSegmentsToKeep(segments, scores, cfg)

	// Build output
	var b strings.Builder
	totalKept := 0
	prevIdx := -1

	for i, seg := range segments {
		if !keepSet[i] {
			continue
		}
		if prevIdx >= 0 && i > prevIdx+1 {
			// Add omission marker for skipped segments
			omitted := seg.Index - prevIdx - 1
			b.WriteString("\n\n")
			b.WriteString(sprintfOmission(omitted))
			b.WriteString("\n\n")
		} else if prevIdx >= 0 && i == prevIdx+1 && b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(seg.Text)
		totalKept++
		prevIdx = seg.Index
	}

	// Check if we need to truncate further
	result := b.String()
	if utf8.RuneCountInString(result) > cfg.MaxRunes {
		result = truncateToBudget(result, cfg.MaxRunes)
	}

	if totalKept < len(segments) {
		keptCount := totalKept
		result += sprintfShardNote(keptCount, len(segments))
	}

	return result
}

// splitIntoSegments splits text into logical paragraphs.
func splitIntoSegments(text string) []ShardSegment {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Try paragraph split first (double newline)
	paragraphs := strings.Split(text, "\n\n")
	if len(paragraphs) >= 3 {
		return toSegments(paragraphs)
	}

	// Fallback: split by single newline for code-heavy content
	lines := strings.Split(text, "\n")
	if len(lines) <= 3 {
		return []ShardSegment{{Text: text, Index: 0}}
	}

	// Group consecutive lines into segments of ~500 runes each
	var segments []ShardSegment
	var current strings.Builder
	runeBudget := 500

	for _, line := range lines {
		if current.Len() > 0 && utf8.RuneCountInString(current.String())+utf8.RuneCountInString(line) > runeBudget {
			segments = append(segments, ShardSegment{Text: strings.TrimSpace(current.String())})
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		segments = append(segments, ShardSegment{Text: strings.TrimSpace(current.String())})
	}

	if len(segments) == 0 {
		return []ShardSegment{{Text: text, Index: 0}}
	}
	return segments
}

func toSegments(paragraphs []string) []ShardSegment {
	var segs []ShardSegment
	for _, p := range paragraphs {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			segs = append(segs, ShardSegment{Text: trimmed})
		}
	}
	if len(segs) == 0 {
		return []ShardSegment{{Text: strings.TrimSpace(strings.Join(paragraphs, "\n\n")), Index: 0}}
	}
	return segs
}

// isImportant checks if a segment contains key information markers.
func isImportant(text string) bool {
	for _, re := range importantPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// selectSegmentsToKeep chooses which segments to preserve.
func selectSegmentsToKeep(segments []ShardSegment, scores []int, cfg ShardConfig) map[int]bool {
	keep := make(map[int]bool)
	n := len(segments)

	// Always keep head segments
	headCount := cfg.HeadSegments
	if headCount > n {
		headCount = n
	}
	for i := 0; i < headCount; i++ {
		keep[i] = true
	}

	// Always keep tail segments
	tailCount := cfg.TailSegments
	if tailCount > n-headCount {
		tailCount = n - headCount
	}
	for i := n - tailCount; i < n; i++ {
		keep[i] = true
	}

	// Fill remaining slots with highest-scoring middle segments
	remaining := cfg.MaxSegments - len(keep)
	if remaining <= 0 {
		return keep
	}

	// Collect middle segment indices and sort by score desc
	type idxScore struct {
		index int
		score int
	}
	var middle []idxScore
	for i := headCount; i < n-tailCount; i++ {
		if !keep[i] {
			middle = append(middle, idxScore{index: i, score: scores[i]})
		}
	}

	// Sort by score descending (bubble sort for small n)
	for i := 0; i < len(middle)-1; i++ {
		for j := i + 1; j < len(middle); j++ {
			if middle[j].score > middle[i].score {
				middle[i], middle[j] = middle[j], middle[i]
			}
		}
	}

	// Take top N
	for i := 0; i < remaining && i < len(middle); i++ {
		keep[middle[i].index] = true
	}

	return keep
}

// truncateToBudget further truncates sharded text to fit within rune budget.
func truncateToBudget(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}

	// Strategy: keep head 60% + tail 40% with omission marker
	headRunes := maxRunes * 3 / 5
	tailRunes := maxRunes - headRunes
	if headRunes < 1 {
		headRunes = 1
	}
	if tailRunes < 1 {
		tailRunes = 1
	}

	omitted := len(runes) - headRunes - tailRunes
	marker := sprintfOmission(omitted)
	// Account for marker length in budget
	markerRunes := []rune(marker)
	headRunes -= len(markerRunes) / 2
	tailRunes -= len(markerRunes) / 2

	if headRunes < 1 {
		headRunes = maxRunes / 2
		tailRunes = maxRunes - headRunes
	}

	return string(runes[:headRunes]) + marker + string(runes[len(runes)-tailRunes:])
}

func sprintfOmission(skipped int) string {
	if skipped == 1 {
		return "…[1 segment omitted]…"
	}
	return "…[" + strconv.Itoa(skipped) + " segments omitted]…"
}

func sprintfShardNote(kept, total int) string {
	return "\n\n[SHARDED: kept " + strconv.Itoa(kept) + "/" + strconv.Itoa(total) + " segments to fit context budget]"
}
