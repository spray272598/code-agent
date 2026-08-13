package intent

import (
	"context"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/deepagent"
)

// SkillMatcher 抽象 Skill 匹配能力（避免直接依赖 skill.Service）
type SkillMatcher interface {
	Match(userInput string) interface{ GetID() string }
}

// Classifier 基于规则的意图分类器（规则快速路径 + LLM 语义兜底）
type Classifier struct {
	skillMatch func(string) string // 返回匹配的 Skill ID，空表示无匹配
	llm        port.ILLMPort
}

// NewClassifier 创建分类器
// skillMatch: 传入 skill.Service.Match 的适配函数，返回 Skill ID 或空字符串
func NewClassifier(skillMatch func(string) string) *Classifier {
	return &Classifier{skillMatch: skillMatch}
}

// SetLLM 注入 LLM 用于规则未命中时的语义兜底分类。
func (c *Classifier) SetLLM(llm port.ILLMPort) { c.llm = llm }

func (c *Classifier) Classify(userInput string) Result {
	trimmed := strings.TrimSpace(userInput)
	low := strings.ToLower(trimmed)

	// 1. 继续执行检测（最高优先级）
	if isContinue(low) {
		return Result{Intent: IntentContinue, CleanInput: trimmed, Confidence: 1.0, Source: "keyword"}
	}

	// 2. Deep 前缀检测
	if deepagent.LooksDeep(trimmed) {
		clean := deepagent.StripPrefix(trimmed)
		return Result{Intent: IntentDeep, CleanInput: clean, Confidence: 1.0, Source: "prefix"}
	}

	// 3. Team 前缀检测
	if looksMulti(low) {
		clean := stripMultiPrefix(trimmed)
		return Result{Intent: IntentTeam, CleanInput: clean, Confidence: 1.0, Source: "prefix"}
	}

	// 4. LLM 语义兜底：识别规则覆盖不到的长尾自然语言
	if c.llm != nil {
		if it, conf := c.classifyLLM(trimmed); it != IntentNormal && conf >= 0.7 {
			return Result{Intent: it, CleanInput: trimmed, Confidence: conf, Source: "llm"}
		}
	}

	// 5. 默认：普通对话
	return Result{Intent: IntentNormal, CleanInput: trimmed, Confidence: 0.5, Source: "default"}
}

const intentSystemPrompt = `Classify the user's intent into ONE of:
- "deep": a complex multi-step coding/implementation task needing plan→act→reflect
- "team": a task that benefits from parallel multi-agent roles (research/explore in parallel)
- "normal": ordinary single-turn conversation or a simple task

Reply with STRICT JSON: {"intent":"<deep|team|normal>"}`

func (c *Classifier) classifyLLM(userInput string) (Intent, float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := c.llm.Generate(ctx, &port.ChatRequest{
		SystemPrompt: intentSystemPrompt,
		Messages:     []port.ChatMessage{{Role: "user", Content: userInput}},
		Temperature:  0.1,
		MaxTokens:    24,
	})
	if err != nil || resp == nil {
		return IntentNormal, 0
	}
	switch parseIntent(resp.Content) {
	case "deep":
		return IntentDeep, 0.8
	case "team":
		return IntentTeam, 0.8
	default:
		return IntentNormal, 0
	}
}

// parseIntent extracts {"intent":"..."} from an LLM reply.
func parseIntent(content string) string {
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return ""
	}
	// lightweight manual parse avoids importing encoding/json just for one field
	body := content[start+1 : end]
	for _, part := range strings.Split(body, ",") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.Trim(strings.TrimSpace(kv[0]), `"`)
		v := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		if k == "intent" {
			return strings.ToLower(v)
		}
	}
	return ""
}

func isContinue(low string) bool {
	return low == "继续" || low == "continue" || low == "ok" || low == "y" || low == "yes" || low == "继续执行"
}

func looksMulti(low string) bool {
	return strings.HasPrefix(low, "/team") || strings.HasPrefix(low, "/parallel") ||
		strings.Contains(low, "parallel explore") || strings.Contains(low, "team mode")
}

func stripMultiPrefix(s string) string {
	raw := strings.TrimSpace(s)
	low := strings.ToLower(raw)
	prefixes := []string{"/team", "/parallel"}
	for _, p := range prefixes {
		if strings.HasPrefix(low, p) {
			return strings.TrimSpace(raw[len(p):])
		}
	}
	return raw
}
