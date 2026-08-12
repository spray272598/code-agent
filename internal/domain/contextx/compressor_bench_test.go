package contextx

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spray272598/code-agent/internal/types/common"
)

// 模拟一轮对话的3条消息：user提问 + assistant工具调用 + tool结果
func makeRound(i int) []map[string]any {
	// user 消息：约 100 字符
	userContent := fmt.Sprintf("请帮我查看第%d个文件的实现，分析代码逻辑并给出优化建议", i)
	// assistant 消息：工具调用描述，约 150 字符
	assistantContent := fmt.Sprintf("我来读取文件 src/module_%d/handler.go 的内容，分析其中的业务逻辑和潜在问题。Thought: 用户要求查看第%d个文件", i, i)
	// tool 结果：模拟 read_file 返回的代码内容，约 3000 字符（超过 L0 的 2000 字符阈值）
	codeLines := make([]string, 60)
	for j := 0; j < 60; j++ {
		codeLines[j] = fmt.Sprintf("  line%d := someFunction(param%d, param%d) // 这是一个示例代码行，模拟真实业务逻辑", j, j, i)
	}
	toolContent := strings.Join(codeLines, "\n")

	return []map[string]any{
		{"role": "user", "content": userContent},
		{"role": "assistant", "content": assistantContent},
		{"role": "tool", "content": toolContent},
	}
}

// 构造 N 轮对话历史
func makeHistory(rounds int) []map[string]any {
	var history []map[string]any
	for i := 1; i <= rounds; i++ {
		history = append(history, makeRound(i)...)
	}
	return history
}

func TestCompressionBench(t *testing.T) {
	// 测试不同对话轮数下的压缩效果
	scenarios := []struct {
		rounds      int
		tokenBudget int
		desc        string
	}{
		{10, 8000, "10轮对话(30条消息)"},
		{20, 8000, "20轮对话(60条消息)"},
		{30, 8000, "30轮对话(90条消息)"},
		{20, 4000, "20轮+小预算(4000)"},
		{20, 16000, "20轮+大预算(16000)"},
	}

	fmt.Println("\n╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║           上下文压缩基准测试 (Context Compression Benchmark)          ║")
	fmt.Println("╠══════════╦═════════╦═════════╦═════════╦══════════╦═════════════════╣")
	fmt.Println("║ 场景      ║ 原始msg ║ 压缩后  ║ 原始tok ║ 压缩后tok ║ 压缩率  ║ 级别  ║")
	fmt.Println("╠══════════╬═════════╬═════════╬═════════╬══════════╬══════════╬═══════╣")

	for _, sc := range scenarios {
		history := makeHistory(sc.rounds)
		originalTokens := estimateHistory(history)
		originalMsgs := len(history)

		c := NewCompressor(sc.tokenBudget)
		result := c.CompressLevels(t.Context(), history, "", false)

		compressedTokens := estimateHistory(result.History)
		compressedMsgs := len(result.History)
		reduction := 0.0
		if originalTokens > 0 {
			reduction = float64(originalTokens-compressedTokens) / float64(originalTokens) * 100
		}

		fmt.Printf("║ %-8s ║ %7d ║ %7d ║ %7d ║ %8d ║ %6.1f%% ║ %-5s ║\n",
			sc.desc, originalMsgs, compressedMsgs, originalTokens, compressedTokens, reduction, result.Level)
	}

	fmt.Println("╚══════════╩═════════╩═════════╩═════════╩══════════╩══════════╩═══════╝")
}

func TestTokenManagerBench(t *testing.T) {
	// 测试 TokenManager.TrimMessages 的紧急截断效果
	fmt.Println("\n╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║      Token 紧急截断测试 (TokenManager.TrimMessages)      ║")
	fmt.Println("╠══════════════════════╦═════════╦═════════╦═══════════════╣")
	fmt.Println("║ 消息数                ║ 原始tok ║ 截断后  ║ 压缩率        ║")
	fmt.Println("╠══════════════════════╬═════════╬═════════╬═══════════════╣")

	for _, rounds := range []int{5, 10, 20, 30, 50} {
		history := makeHistory(rounds)

		// 转换为 port.ChatMessage 格式
		msgs := make([]struct {
			Role    string
			Content string
		}, len(history))
		totalTokens := 0
		for i, m := range history {
			content, _ := m["content"].(string)
			msgs[i].Role, _ = m["role"].(string)
			msgs[i].Content = content
			totalTokens += common.EstimateTokens(content)
		}

		// 模拟 TrimMessages：保留 head 2 + tail 6
		keepTail := 6
		var trimmedTokens int
		if len(history) > keepTail+2 {
			head := history[:2]
			tail := history[len(history)-keepTail:]
			trimmed := append(head, tail...)
			for _, m := range trimmed {
				if c, ok := m["content"].(string); ok {
					trimmedTokens += common.EstimateTokens(c)
				}
			}
		} else {
			trimmedTokens = totalTokens
		}

		reduction := 0.0
		if totalTokens > 0 {
			reduction = float64(totalTokens-trimmedTokens) / float64(totalTokens) * 100
		}

		fmt.Printf("║ %-18d ║ %7d ║ %7d ║ %6.1f%%       ║\n",
			len(history), totalTokens, trimmedTokens, reduction)
	}

	fmt.Println("╚══════════════════════╩═════════╩═════════╩═══════════════╝")
}

func TestL0TruncationEffect(t *testing.T) {
	// 单独测试 L0 截断长内容的效果
	fmt.Println("\n╔════════════════════════════════════════════════════════╗")
	fmt.Println("║     L0 长内容截断测试 (CompressLongContentMaxRunes)    ║")
	fmt.Println("╠═════════════════════╦══════════╦═════════╦════════════╣")
	fmt.Println("║ 内容长度(字符)       ║ 原始tok  ║ 截断后  ║ 压缩率     ║")
	fmt.Println("╠═════════════════════╬══════════╬═════════╬════════════╣")

	for _, charLen := range []int{500, 1000, 2000, 3000, 5000, 10000} {
		content := strings.Repeat("x", charLen)
		original := common.EstimateTokens(content)
		truncated := common.TruncateRunes(content, common.CompressLongContentMaxRunes)
		after := common.EstimateTokens(truncated)
		reduction := 0.0
		if original > 0 {
			reduction = float64(original-after) / float64(original) * 100
		}
		fmt.Printf("║ %-17d ║ %8d ║ %7d ║ %8.1f%%  ║\n", charLen, original, after, reduction)
	}

	fmt.Println("╚═════════════════════╩══════════╩═════════╩════════════╝")
}
