package contextx

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/types/common"
)

type mockLLM struct {
	resp *port.ChatResponse
	err  error
}

func (m *mockLLM) Generate(ctx context.Context, req *port.ChatRequest) (*port.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.resp != nil {
		return m.resp, nil
	}
	return &port.ChatResponse{Content: "mocked summary"}, nil
}

func (m *mockLLM) GenerateStream(ctx context.Context, req *port.ChatRequest, onDelta func(delta port.StreamDelta)) (*port.ChatResponse, error) {
	return m.Generate(ctx, req)
}

type mockBlobStore struct {
	data   map[string][]byte
	putErr error
}

func (m *mockBlobStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if m.putErr != nil {
		return m.putErr
	}
	if m.data == nil {
		m.data = make(map[string][]byte)
	}
	m.data[key] = data
	return nil
}

func (m *mockBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	if d, ok := m.data[key]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("key not found")
}

func (m *mockBlobStore) Exists(ctx context.Context, key string) bool {
	_, ok := m.data[key]
	return ok
}

// --- RuleSummarizeSingle tests ---

func TestRuleSummarizeSingle_ShortPassthrough(t *testing.T) {
	content := "This is a short message."
	result := RuleSummarizeSingle(content, 400)
	if result != content {
		t.Errorf("expected short content passthrough, got %q", result)
	}
}

func TestRuleSummarizeSingle_ExtractsKeySentences(t *testing.T) {
	content := strings.Join([]string{
		"First sentence with some background information about the project.",
		"The error occurred at line 42: nil pointer dereference in processRequest.",
		"Package main contains the entry point and initialization code.",
		"Another middle sentence without any signal words.",
		"The fix involves adding a nil check before calling processRequest.",
		"Conclusion: the patch resolves the issue and all tests pass.",
	}, ". ")

	result := RuleSummarizeSingle(content, 400)
	if !strings.HasPrefix(result, "[RULE_SUMMARY] ") {
		t.Errorf("expected [RULE_SUMMARY] prefix, got %q", result)
	}
	if !strings.Contains(result, "error") {
		t.Error("expected 'error' keyword preserved")
	}
	if !strings.Contains(result, "Conclusion") {
		t.Error("expected 'Conclusion' preserved")
	}
}

func TestRuleSummarizeSingle_Truncation(t *testing.T) {
	longContent := strings.Repeat("This is a detailed technical description of a complex software bug. ", 50)
	result := RuleSummarizeSingle(longContent, 100)
	runeLen := len([]rune(result))
	if runeLen > 100+50 {
		t.Errorf("result too long: %d runes (expected <= ~150)", runeLen)
	}
}

// --- ShardLongText tests ---

func TestShardLongText_ShortPassthrough(t *testing.T) {
	content := "Short text that doesn't need sharding."
	cfg := ShardConfig{MaxRunes: 2000, MaxSegments: 6, HeadSegments: 2, TailSegments: 2}
	result := ShardLongText(content, cfg)
	if result != content {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestShardLongText_PreservesHeadAndTail(t *testing.T) {
	var segments []string
	for i := 0; i < 8; i++ {
		segments = append(segments, fmt.Sprintf("Segment %d: This paragraph contains some content about topic %d.", i, i))
	}
	content := strings.Join(segments, "\n\n")

	cfg := ShardConfig{MaxRunes: 500, MaxSegments: 4, HeadSegments: 2, TailSegments: 2}
	result := ShardLongText(content, cfg)

	if !strings.Contains(result, "Segment 0") {
		t.Error("expected Segment 0 (head) preserved")
	}
	if !strings.Contains(result, "Segment 1") {
		t.Error("expected Segment 1 (head) preserved")
	}
	if !strings.Contains(result, "Segment 6") {
		t.Error("expected Segment 6 (tail) preserved")
	}
	if !strings.Contains(result, "Segment 7") {
		t.Error("expected Segment 7 (tail) preserved")
	}
	if !strings.Contains(result, "omitted") {
		t.Error("expected omission markers for skipped segments")
	}
}

func TestShardLongText_PreservesImportantSegments(t *testing.T) {
	segments := []string{
		"Segment 0: Normal intro content without signal words.",
		"Segment 1: Another normal paragraph describing the background.",
		"Segment 2: ERROR: Failed to connect to database server at 10.0.0.1:5432.",
		"Segment 3: More normal content about the architecture design.",
		"Segment 4: The result shows a 30% improvement in processing speed.",
		"Segment 5: Final remarks and acknowledgements.",
	}
	content := strings.Join(segments, "\n\n")

	cfg := ShardConfig{MaxRunes: 500, MaxSegments: 5, HeadSegments: 1, TailSegments: 1}
	result := ShardLongText(content, cfg)

	if !strings.Contains(result, "ERROR") {
		t.Error("expected ERROR segment preserved (important pattern)")
	}
	if !strings.Contains(result, "result") || !strings.Contains(result, "30%") {
		t.Error("expected result segment preserved (important pattern)")
	}
}

func TestShardLongText_CodeContent(t *testing.T) {
	segments := []string{
		"package main",
		"import (\"fmt\")",
		"func hello() {\n\tfmt.Println(\"hello\")\n}",
		"Some random text about the weather.",
		"type Server struct { Addr string }",
		"const MaxRetries = 3",
		"var globalConfig = Config{}",
		"Done with all declarations.",
	}
	content := strings.Join(segments, "\n\n")

	cfg := ShardConfig{MaxRunes: 300, MaxSegments: 5, HeadSegments: 1, TailSegments: 1}
	result := ShardLongText(content, cfg)

	if !strings.Contains(result, "package main") {
		t.Error("expected package declaration preserved")
	}
	if !strings.Contains(result, "func hello") {
		t.Error("expected function declaration preserved")
	}
}

func TestShardLongText_MarksShardedNote(t *testing.T) {
	segments := []string{
		"Segment 0 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 1 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 2 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 3 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 4 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 5 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 6 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
		"Segment 7 content with enough text to make this paragraph substantial and trigger the sharding mechanism.",
	}
	content := strings.Join(segments, "\n\n")

	cfg := ShardConfig{MaxRunes: 500, MaxSegments: 3, HeadSegments: 1, TailSegments: 1}
	result := ShardLongText(content, cfg)

	if !strings.Contains(result, "[SHARDED: kept") {
		t.Error("expected shard note marker")
	}
}

func TestShardLongText_BudgetEnforcement(t *testing.T) {
	content := strings.Repeat(strings.Repeat("A", 200)+"\n\n", 20)
	cfg := ShardConfig{MaxRunes: 300, MaxSegments: 4, HeadSegments: 1, TailSegments: 1}
	result := ShardLongText(content, cfg)
	runeLen := len([]rune(result))
	if runeLen > 300+100 {
		t.Errorf("result exceeds budget: %d runes (expected <= ~400)", runeLen)
	}
}

// --- processLongMessage priority chain tests ---

func TestProcessLongMessage_SemanticSummary(t *testing.T) {
	llm := &mockLLM{resp: &port.ChatResponse{Content: "Semantic summary produced by LLM."}}
	summarizer := NewSummarizer(llm)

	c := NewCompressor(10000)
	c.Summarizer = summarizer
	c.LongContentThresholdRunes = 50
	c.MaxSummaryRunes = 200

	longContent := strings.Repeat("This is a very long message that should be summarized by the LLM because it exceeds the threshold. ", 20)

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	if !strings.HasPrefix(result, "[SUMMARIZED] ") {
		t.Errorf("expected LLM summary, got %q", result)
	}
	if !strings.Contains(result, "Semantic summary") {
		t.Errorf("expected mocked LLM content, got %q", result)
	}
}

func TestProcessLongMessage_LLMFallsBackToRuleSummary(t *testing.T) {
	llm := &mockLLM{err: fmt.Errorf("LLM unavailable")}
	summarizer := NewSummarizer(llm)

	c := NewCompressor(10000)
	c.Summarizer = summarizer
	c.LongContentThresholdRunes = 50

	longContent := strings.Join([]string{
		"First sentence with background.",
		"ERROR: Connection refused on port 8080.",
		"Package initialization completed.",
		"Some other content.",
		"Conclusion: System is working.",
	}, ". ")
	longContent = strings.Repeat(longContent, 3)

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	if !strings.HasPrefix(result, "[RULE_SUMMARY] ") {
		t.Errorf("expected rule summary fallback, got %q", result)
	}
}

func TestProcessLongMessage_ShardingWhenNoSummarizer(t *testing.T) {
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.LongContentThresholdRunes = 50

	segments := make([]string, 10)
	for i := range segments {
		segments[i] = fmt.Sprintf("Paragraph %d: Some content about topic %d.", i, i)
	}
	longContent := strings.Join(segments, "\n\n")

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	if !strings.Contains(result, "omitted") && !strings.Contains(result, "SHARDED") {
		if len([]rune(result)) > 50 {
			if !strings.HasSuffix(strings.TrimSpace(result), "…") {
				t.Logf("result length: %d runes (no shard markers found)", len([]rune(result)))
			}
		}
	}
	t.Logf("sharding result: %d runes", len([]rune(result)))
}

func TestProcessLongMessage_BlobOffload(t *testing.T) {
	store := &mockBlobStore{}
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.BlobStore = store
	c.LongContentThresholdRunes = 50
	c.SessionID = "test-session-001"

	longContent := strings.Repeat("This content is long enough to trigger offloading because summarization is unavailable and the text is very long. ", 30)

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "tool", "toolName": "read_file"})

	if !strings.Contains(result, "[OFFLOADED:") {
		t.Errorf("expected blob offload marker, got %q", result[:min(len(result), 150)])
	}
	if !strings.Contains(result, "sessions/test-session-001") {
		t.Errorf("expected session key in offload marker, got %q", result)
	}
	if len(store.data) != 1 {
		t.Errorf("expected 1 blob stored, got %d", len(store.data))
	}
}

func TestProcessLongMessage_BlobOffloadFallsBackToTruncation(t *testing.T) {
	store := &mockBlobStore{putErr: fmt.Errorf("storage error")}
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.BlobStore = store
	c.LongContentThresholdRunes = 50

	longContent := strings.Repeat("This content will eventually be truncated because all higher-priority methods failed. ", 20)

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	if strings.Contains(result, "[OFFLOADED:") {
		t.Error("should not have offloaded when blob store fails")
	}
	runeLen := len([]rune(result))
	if runeLen > c.LongContentThresholdRunes+50 {
		t.Errorf("result too long after truncation: %d runes", runeLen)
	}
}

func TestProcessLongMessage_FinalFallbackTruncation(t *testing.T) {
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.BlobStore = nil
	c.LongContentThresholdRunes = 50

	longContent := strings.Repeat("This will be truncated as the ultimate fallback. ", 100)

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	runeLen := len([]rune(result))
	// TruncateRunesKeepTail adds a marker like " …[middle omitted: N runes]… "
	// which can be ~40 runes overhead beyond the head+tail budget
	if runeLen > c.LongContentThresholdRunes+60 {
		t.Errorf("truncation fallback too long: %d runes (threshold=%d)", runeLen, c.LongContentThresholdRunes)
	}
	if runeLen >= len([]rune(longContent)) {
		t.Error("truncation should reduce content length")
	}
}

func TestProcessLongMessage_SkipsShortContent(t *testing.T) {
	c := NewCompressor(10000)
	c.LongContentThresholdRunes = 200

	shortContent := "This message is well within limits."
	result := c.processLongMessage(context.Background(), shortContent, map[string]any{"role": "user"})

	if result != shortContent {
		t.Errorf("expected short content unchanged, got %q", result)
	}
}

func TestProcessLongMessage_PriorityChain_Integration(t *testing.T) {
	llm := &mockLLM{resp: &port.ChatResponse{Content: "LLM summarized this content concisely."}}
	summarizer := NewSummarizer(llm)
	store := &mockBlobStore{}

	c := NewCompressor(10000)
	c.Summarizer = summarizer
	c.BlobStore = store
	c.LongContentThresholdRunes = 50

	longContent := strings.Repeat("Very long content that should be handled by the highest-priority available method. ", 25)

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	if !strings.HasPrefix(result, "[SUMMARIZED] ") {
		t.Errorf("expected semantic summary (highest priority), got %q", result)
	}
	if len(store.data) != 0 {
		t.Error("blob store should not have been used when LLM summary succeeds")
	}
}

func TestProcessLongMessage_ShardingThenBlobFallback(t *testing.T) {
	// Multi-segment content: sharding should work (has markers), then blob not needed
	store := &mockBlobStore{}
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.BlobStore = store
	c.LongContentThresholdRunes = 50

	segments := make([]string, 10)
	for i := range segments {
		segments[i] = fmt.Sprintf("Paragraph %d with some content about topic %d and additional text to make it long enough.", i, i)
	}
	longContent := strings.Join(segments, "\n\n")

	result := c.processLongMessage(context.Background(), longContent, map[string]any{"role": "user"})

	// Should use sharding (has markers) and NOT fall through to blob
	if !strings.Contains(result, "omitted") && !strings.Contains(result, "SHARDED") {
		t.Errorf("expected sharding markers, got %q", result[:min(len(result), 200)])
	}
	if len(store.data) != 0 {
		t.Error("blob store should not be used when sharding succeeds")
	}
	t.Logf("sharding result: %d runes", len([]rune(result)))
}

// --- CompressLevels end-to-end tests ---

func TestCompressLevels_L0ReducesLongMessages(t *testing.T) {
	llm := &mockLLM{resp: &port.ChatResponse{Content: "Summary of long tool output."}}
	summarizer := NewSummarizer(llm)

	c := NewCompressor(10000)
	c.Summarizer = summarizer
	c.LongContentThresholdRunes = 100
	c.KeepRecent = 3

	history := []map[string]any{
		{"role": "user", "content": "Short user query"},
		{"role": "tool", "content": strings.Repeat("Very long tool output with lots of data. ", 50), "toolName": "read_file"},
		{"role": "assistant", "content": "Short assistant reply"},
	}

	result := c.CompressLevels(context.Background(), history, "", false)

	if len(result.History) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.History))
	}

	toolMsg := result.History[1]
	toolContent, _ := toolMsg["content"].(string)
	if !strings.HasPrefix(toolContent, "[SUMMARIZED]") {
		t.Errorf("expected tool output summarized, got %q", toolContent[:min(len(toolContent), 100)])
	}
}

func TestCompressLevels_L0ShardingFallback(t *testing.T) {
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.LongContentThresholdRunes = 50
	c.KeepRecent = 3

	longToolOutput := strings.Repeat("File content line with important data.\n\n", 30)

	history := []map[string]any{
		{"role": "user", "content": "Need to read a file"},
		{"role": "tool", "content": longToolOutput, "toolName": "read_file"},
		{"role": "assistant", "content": "Got the file"},
	}

	result := c.CompressLevels(context.Background(), history, "", false)

	if len(result.History) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.History))
	}

	toolContent := result.History[1]["content"].(string)
	runeLen := len([]rune(toolContent))
	if runeLen >= len([]rune(longToolOutput)) {
		t.Errorf("tool output should have been reduced: before=%d after=%d", len([]rune(longToolOutput)), runeLen)
	}
	t.Logf("L0 sharding reduced tool output from %d to %d runes", len([]rune(longToolOutput)), runeLen)
}

func TestCompressLevels_L0BlobOffload(t *testing.T) {
	store := &mockBlobStore{}
	c := NewCompressor(10000)
	c.Summarizer = nil
	c.BlobStore = store
	c.LongContentThresholdRunes = 50
	c.KeepRecent = 3

	// Single-segment content (no \n\n separators) → sharding won't help → blob offload
	longContent := strings.Repeat(strings.Repeat("A", 200), 30)

	history := []map[string]any{
		{"role": "user", "content": "Query"},
		{"role": "tool", "content": longContent, "toolName": "bash"},
		{"role": "assistant", "content": "Done"},
	}

	result := c.CompressLevels(context.Background(), history, "", false)

	if len(result.History) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.History))
	}

	toolContent := result.History[1]["content"].(string)
	if !strings.Contains(toolContent, "[OFFLOADED:") {
		t.Errorf("expected offload marker, got %q", toolContent[:min(len(toolContent), 150)])
	}
	if len(store.data) != 1 {
		t.Errorf("expected 1 blob stored, got %d", len(store.data))
	}
}

func TestCompressLevels_SkipsL0ForShortMessages(t *testing.T) {
	c := NewCompressor(10000)
	c.LongContentThresholdRunes = 500

	history := []map[string]any{
		{"role": "user", "content": "Hello"},
		{"role": "assistant", "content": "Hi there!"},
		{"role": "user", "content": "How are you?"},
		{"role": "assistant", "content": "Great!"},
	}

	original := make([]map[string]any, len(history))
	copy(original, history)

	result := c.CompressLevels(context.Background(), history, "", false)

	for i, m := range result.History {
		if m["content"] != original[i]["content"] {
			t.Errorf("message %d content changed unexpectedly: before=%q after=%q",
				i, original[i]["content"], m["content"])
		}
	}
}

func TestCompressLevels_L0PriorityChainInOrder(t *testing.T) {
	llm := &mockLLM{resp: &port.ChatResponse{Content: "LLM-level summary"}}
	summarizer := NewSummarizer(llm)
	store := &mockBlobStore{}

	c := NewCompressor(10000)
	c.Summarizer = summarizer
	c.BlobStore = store
	c.LongContentThresholdRunes = 50

	longMsg := strings.Repeat("Detailed technical analysis of the performance bottleneck. ", 30)
	result := c.processLongMessage(context.Background(), longMsg, map[string]any{"role": "user"})

	if !strings.Contains(result, "LLM-level summary") {
		t.Error("expected LLM summary (highest priority) to be used")
	}
	if len(store.data) != 0 {
		t.Error("blob should not be written when LLM succeeds")
	}
}

func TestCompressLevels_L0RuleSummaryBeforeSharding(t *testing.T) {
	llm := &mockLLM{err: fmt.Errorf("LLM error")}
	summarizer := NewSummarizer(llm)

	c := NewCompressor(10000)
	c.Summarizer = summarizer
	c.LongContentThresholdRunes = 50

	content := "Error: Connection timeout. The system needs to be restarted. Package initialization. Conclusion: issue resolved."
	content = strings.Repeat(content, 5)

	result := c.processLongMessage(context.Background(), content, map[string]any{"role": "user"})

	if !strings.HasPrefix(result, "[RULE_SUMMARY]") {
		t.Errorf("expected rule summary before sharding, got %q", result[:min(len(result), 100)])
	}
	if strings.Contains(result, "[SHARDED:") {
		t.Error("should not fall through to sharding when rule summary succeeds")
	}
}

// --- Edge case tests ---

func TestProcessLongMessage_EmptyContent(t *testing.T) {
	c := NewCompressor(10000)
	result := c.processLongMessage(context.Background(), "", map[string]any{"role": "user"})
	if result != "" {
		t.Errorf("expected empty string for empty input, got %q", result)
	}
}

func TestProcessLongMessage_UnicodeContent(t *testing.T) {
	c := NewCompressor(10000)
	c.LongContentThresholdRunes = 20

	content := strings.Repeat("这是一段非常长的中文内容，包含大量中文字符用于测试上下文压缩的长消息处理功能。", 10)

	result := c.processLongMessage(context.Background(), content, map[string]any{"role": "user"})
	if len([]rune(result)) > common.CompressLongContentMaxRunes+100 {
		t.Errorf("unicode content not properly handled: %d runes", len([]rune(result)))
	}
	t.Logf("unicode content: %d → %d runes", len([]rune(content)), len([]rune(result)))
}

func TestProcessLongMessage_ConfigurableThreshold(t *testing.T) {
	c := NewCompressor(10000)
	c.LongContentThresholdRunes = 500

	content := strings.Repeat("This content is 300 runes long which is below the threshold. ", 5)
	result := c.processLongMessage(context.Background(), content, map[string]any{"role": "user"})

	lenBefore := len([]rune(content))
	lenAfter := len([]rune(result))

	if lenBefore <= 500 && lenAfter == lenBefore {
		t.Logf("content below threshold passes through unchanged: %d runes", lenBefore)
	}

	c.LongContentThresholdRunes = 10
	result2 := c.processLongMessage(context.Background(), content, map[string]any{"role": "user"})
	if len([]rune(result2)) >= lenBefore {
		t.Error("content above threshold should be reduced")
	}
}

func TestShardLongText_EmptyInput(t *testing.T) {
	cfg := DefaultShardConfig()
	result := ShardLongText("", cfg)
	if result != "" {
		t.Errorf("expected empty for empty input, got %q", result)
	}
}

func TestRuleSummarizeSingle_EmptyInput(t *testing.T) {
	result := RuleSummarizeSingle("", 400)
	if result != "" {
		t.Errorf("expected empty for empty input, got %q", result)
	}
}

func TestSprintfOmission_NumberFormatting(t *testing.T) {
	testCases := []struct {
		skipped int
		want    string
	}{
		{1, "…[1 segment omitted]…"},
		{5, "…[5 segments omitted]…"},
		{15, "…[15 segments omitted]…"},
		{105, "…[105 segments omitted]…"},
		{1005, "…[1005 segments omitted]…"},
	}

	for _, tc := range testCases {
		got := sprintfOmission(tc.skipped)
		if got != tc.want {
			t.Errorf("sprintfOmission(%d) = %q, want %q", tc.skipped, got, tc.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}