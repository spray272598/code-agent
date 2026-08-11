package intent

import (
	"strings"

	"github.com/spray272598/code-agent/internal/domain/deepagent"
)

// SkillMatcher 抽象 Skill 匹配能力（避免直接依赖 skill.Service）
type SkillMatcher interface {
	Match(userInput string) interface{ GetID() string }
}

// Classifier 基于规则的意图分类器
type Classifier struct {
	skillMatch func(string) string // 返回匹配的 Skill ID，空表示无匹配
}

// NewClassifier 创建分类器
// skillMatch: 传入 skill.Service.Match 的适配函数，返回 Skill ID 或空字符串
func NewClassifier(skillMatch func(string) string) *Classifier {
	return &Classifier{skillMatch: skillMatch}
}

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

	// 4. 默认：普通对话
	return Result{Intent: IntentNormal, CleanInput: trimmed, Confidence: 0.5, Source: "default"}
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
