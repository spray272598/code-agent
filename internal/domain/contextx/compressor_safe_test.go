package contextx

import (
	"testing"

	"github.com/spray272598/code-agent/internal/types/common"
)

func estimateMsg(m map[string]any) int {
	content, _ := m["content"].(string)
	return common.EstimateTokens(content)
}

// TestSafeSplitNormalNoTools verifies that SelectSafeSplit works identically to
// a simple token-based split when no tool pairs exist.
func TestSafeSplitNormalNoTools(t *testing.T) {
	history := []map[string]any{
		{"role": "user", "content": "msg1"},
		{"role": "assistant", "content": "msg2"},
		{"role": "user", "content": "msg3"},
		{"role": "assistant", "content": "msg4"},
		{"role": "user", "content": "msg5"},
		{"role": "assistant", "content": "msg6"},
	}

	for _, tc := range []struct {
		name         string
		targetTokens int
		wantCut      int
	}{
		{"target 0 → last msg only", 0, 5},
		{"target huge → all in recent", 10000, 0},
		{"target small → last msg", 1, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectSafeSplit(history, tc.targetTokens)
			if got != tc.wantCut {
				t.Errorf("SelectSafeSplit(target=%d) = %d, want %d", tc.targetTokens, got, tc.wantCut)
			}
		})
	}
}

// TestSafeSplitSnapsOnToolResult verifies that when the cut point lands
// directly on a tool-result message, it snaps backward to include the
// preceding tool-request message.
func TestSafeSplitSnapsOnToolResult(t *testing.T) {
	history := []map[string]any{
		{"role": "user", "content": "query"},
		{"role": "assistant", "content": "Action: read_file(path=/a/b.go)"}, // tool request
		{"role": "tool", "content": "file content result"},                    // tool result
		{"role": "user", "content": "follow up"},
		{"role": "assistant", "content": "normal reply"},
	}

	// Compute targetTokens so the cut lands exactly on the tool result (index 2).
	// Need: tokens[4] + tokens[3] < targetTokens <= tokens[4] + tokens[3] + tokens[2]
	tokens34 := estimateMsg(history[4]) + estimateMsg(history[3])
	target := tokens34 + 1
	if target > tokens34+estimateMsg(history[2]) {
		t.Fatalf("calculation error: target=%d should be <= %d", target, tokens34+estimateMsg(history[2]))
	}

	got := SelectSafeSplit(history, target)

	if got < 0 || got >= len(history) {
		t.Fatalf("cut=%d out of range [0,%d)", got, len(history))
	}

	if isToolResult(history[got]) {
		t.Errorf("cut landed on tool result at index %d, should have snapped backward", got)
	}

	if !isToolRequest(history[got]) {
		t.Errorf("expected cut to snap to tool-request boundary, got cut=%d (msg=%v)", got, history[got])
	}

	if got+1 >= len(history) || !isToolResult(history[got+1]) {
		t.Errorf("expected tool result at cut+1 %d, got %v", got+1, history[min(got+1, len(history)-1)])
	}
}

// TestSafeSplitSnapsOnToolRequestBoundary verifies that when the cut lands
// such that a tool request would be the last message in "middle" and its
// result the first in "recent", the snap pulls both into the same partition.
func TestSafeSplitSnapsOnToolRequestBoundary(t *testing.T) {
	history := []map[string]any{
		{"role": "user", "content": "msg0"},
		{"role": "assistant", "content": "normal response"},
		{"role": "user", "content": "msg2"},
		{"role": "assistant", "content": "Action: search_code(q=foo)"}, // tool request
		{"role": "tool", "content": "search result lines"},                 // tool result
		{"role": "user", "content": "msg5"},
		{"role": "assistant", "content": "msg6"},
	}

	// Compute targetTokens so the cut lands on tool result (index 4).
	// Need: tokens[6]+tokens[5] < target <= tokens[6]+tokens[5]+tokens[4]
	tailSum := estimateMsg(history[6]) + estimateMsg(history[5])
	target := tailSum + 1

	got := SelectSafeSplit(history, target)

	if got > 3 {
		t.Errorf("cut=%d splits tool pair; expected <= 3 to keep request(at 3)+result(at 4) together", got)
	}
	if isToolResult(history[got]) {
		t.Errorf("cut still lands on tool result at index %d", got)
	}
}

// TestSafeSplitMultipleConsecutiveToolResults verifies that multiple
// consecutive tool results are handled correctly — the snap should include
// all of them plus the preceding tool request.
func TestSafeSplitMultipleConsecutiveToolResults(t *testing.T) {
	history := []map[string]any{
		{"role": "user", "content": "query"},
		{"role": "assistant", "content": "tool_calls: [call1, call2, call3]"}, // tool request
		{"role": "tool", "content": "result one"},                              // tool result 1
		{"role": "tool", "content": "result two"},                              // tool result 2
		{"role": "tool", "content": "result three"},                            // tool result 3
		{"role": "user", "content": "follow up"},
		{"role": "assistant", "content": "final reply"},
	}

	// Compute targetTokens so the cut lands on the last tool result (index 4).
	// Need: tokens[6]+tokens[5] < target <= tokens[6]+tokens[5]+tokens[4]
	tailSum := estimateMsg(history[6]) + estimateMsg(history[5])
	target := tailSum + 1

	got := SelectSafeSplit(history, target)

	if got > 1 {
		t.Errorf("cut=%d does not include all tool results + request; expected <= 1", got)
	}

	for i := 2; i <= 4; i++ {
		if i < got {
			t.Errorf("tool result at index %d is in middle (cut=%d), should be in recent", i, got)
		}
	}
}

// TestSafeSplitMixedMessages verifies correct behavior with interleaved
// normal messages and tool pairs.
func TestSafeSplitMixedMessages(t *testing.T) {
	history := []map[string]any{
		{"role": "user", "content": "u1"},
		{"role": "assistant", "content": "a1 normal"},
		{"role": "user", "content": "u2"},
		{"role": "assistant", "content": "Action: grep(pattern=foo)"}, // tool request
		{"role": "tool", "content": "grep output lines"},                // tool result
		{"role": "user", "content": "u3"},
		{"role": "assistant", "content": "a3 normal"},
		{"role": "user", "content": "u4"},
		{"role": "assistant", "content": "a4 normal"},
	}

	// Compute targetTokens so the cut lands on tool result (index 4).
	// Need: sum of tokens[8..5] < target <= sum of tokens[8..4]
	tailSum := 0
	for i := 8; i >= 5; i-- {
		tailSum += estimateMsg(history[i])
	}
	target := tailSum + 1

	got := SelectSafeSplit(history, target)

	if got > 3 {
		t.Errorf("cut=%d splits tool pair (request@3, result@4); expected <= 3", got)
	}
	if isToolResult(history[got]) {
		t.Errorf("cut=%d still on tool result after snap", got)
	}
}

// TestSafeSplitAllTools verifies the edge case where all messages form
// tool pairs.
func TestSafeSplitAllTools(t *testing.T) {
	history := []map[string]any{
		{"role": "assistant", "content": "Action: read_file(a)"},
		{"role": "tool", "content": "result a"},
		{"role": "assistant", "content": "Action: read_file(b)"},
		{"role": "tool", "content": "result b"},
		{"role": "assistant", "content": "Action: read_file(c)"},
		{"role": "tool", "content": "result c"},
	}

	// Large targetTokens → all in recent, cut = 0
	got := SelectSafeSplit(history, 10000)
	if got != 0 {
		t.Errorf("all tools, large target: got cut=%d, want 0", got)
	}

	// Small targetTokens → only last pair
	lastPairTokens := estimateMsg(history[4]) + estimateMsg(history[5])
	got = SelectSafeSplit(history, lastPairTokens)
	if got < 0 || got > len(history) {
		t.Errorf("cut=%d out of range", got)
	}

	for i := 0; i < len(history); i++ {
		if isToolResult(history[i]) {
			if i >= got && i-1 < got {
				t.Errorf("tool result at %d is in recent but request at %d is in middle (cut=%d)", i, i-1, got)
			}
		}
		if isToolRequest(history[i]) && i+1 < len(history) && isToolResult(history[i+1]) {
			if i >= got && i+1 < got {
				t.Errorf("tool request at %d is in recent but result at %d is in middle (cut=%d)", i, i+1, got)
			}
		}
	}
}

// TestSafeSplitEmpty verifies empty input handling.
func TestSafeSplitEmpty(t *testing.T) {
	got := SelectSafeSplit(nil, 100)
	if got != 0 {
		t.Errorf("nil history: got %d, want 0", got)
	}
	got = SelectSafeSplit([]map[string]any{}, 100)
	if got != 0 {
		t.Errorf("empty history: got %d, want 0", got)
	}
}

// TestSafeSplitToolRequestAtStart verifies that when a tool request is the
// very first message (index 0), SelectSafeSplit does not try to snap before index 0.
func TestSafeSplitToolRequestAtStart(t *testing.T) {
	history := []map[string]any{
		{"role": "assistant", "content": "Action: read_file(x)"}, // index 0: tool request
		{"role": "tool", "content": "result"},                     // index 1: tool result
		{"role": "user", "content": "u"},                          // index 2
	}

	// Compute targetTokens so the cut lands on tool result (index 1).
	// Need: tokens[2] < target <= tokens[2]+tokens[1]
	t2 := estimateMsg(history[2])
	t1 := estimateMsg(history[1])
	target := t2 + 1
	if target > t2+t1 {
		t.Fatalf("calculation error: target=%d > %d", target, t2+t1)
	}

	got := SelectSafeSplit(history, target)

	if got != 0 {
		t.Errorf("expected cut=0 (tool request at start), got %d", got)
	}
}

// TestSafeSplitIntegration verifies the full integration with CompressLevels,
// ensuring that KeepRecent is respected as a minimum and tool pairs are not split.
func TestSafeSplitIntegration(t *testing.T) {
	history := []map[string]any{
		{"role": "user", "content": "user msg 1"},
		{"role": "assistant", "content": "assistant normal 1"},
		{"role": "user", "content": "user msg 2"},
		{"role": "assistant", "content": "Action: read_file(path=foo.go)"},
		{"role": "tool", "content": "package main\nimport fmt\nfunc main()"},
		{"role": "user", "content": "user msg 3"},
		{"role": "assistant", "content": "assistant normal 3"},
		{"role": "user", "content": "user msg 4"},
	}

	c := NewCompressor(500)
	c.KeepRecent = 3
	r := c.CompressLevels(nil, history, "", false)

	if len(r.History) < c.KeepRecent {
		t.Errorf("KeepRecent=%d not respected, got %d messages", c.KeepRecent, len(r.History))
	}

	for i, m := range r.History {
		if isToolResult(m) {
			if i > 0 && isToolRequest(r.History[i-1]) {
				continue
			}
			t.Errorf("tool result at index %d has no preceding tool request in output", i)
		}
	}
}

// TestSafeSplitFindToolGroupEnd verifies the helper function.
func TestSafeSplitFindToolGroupEnd(t *testing.T) {
	history := []map[string]any{
		{"role": "tool", "content": "r1"},
		{"role": "tool", "content": "r2"},
		{"role": "tool", "content": "r3"},
		{"role": "user", "content": "u"},
	}

	end := findToolGroupEnd(history, 0)
	if end != 3 {
		t.Errorf("findToolGroupEnd(start=0) = %d, want 3", end)
	}

	end = findToolGroupEnd(history, 1)
	if end != 3 {
		t.Errorf("findToolGroupEnd(start=1) = %d, want 3", end)
	}

	end = findToolGroupEnd(history, 3)
	if end != 3 {
		t.Errorf("findToolGroupEnd(start=3) = %d, want 3 (first non-tool)", end)
	}

	end = findToolGroupEnd(history, 4)
	if end != 4 {
		t.Errorf("findToolGroupEnd(start=4) = %d, want 4", end)
	}
}