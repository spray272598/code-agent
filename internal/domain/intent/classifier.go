package intent

import (
	"context"
	"regexp"
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
	return c.ClassifyWithContext(userInput, nil)
}

// EntityContext 携带跨轮对话中"最近被引用/使用的实体"，用于指代消解。
// 由调用方按会话维护（分类器本身是进程级单例，不持有跨用户记忆）。
type EntityContext struct {
	LastSSHConnection string // 最近使用的 SSH 连接名
	LastFile          string // 最近操作的文件
	LastDir           string // 最近操作的目录
	LastSessionID     string // 最近的交互式终端会话 ID
}

// ClassifyWithContext 在 Classify 基础上做指代消解：把"那台机器""刚才那个文件"
// 等代词解析为 EntityContext 中最近记录的实体，结果写入 CleanInput，便于下游
// 工具/LLM 选择正确目标。ec 为 nil 时等价于 Classify。
func (c *Classifier) ClassifyWithContext(userInput string, ec *EntityContext) Result {
	res := c.classifyOnly(userInput)
	if ec != nil {
		if resolved, ok := resolveCoreference(res.CleanInput, ec); ok {
			res.CleanInput = resolved
			res.Source = res.Source + "+coref"
		}
	}
	return res
}

func (c *Classifier) classifyOnly(userInput string) Result {
	trimmed := strings.TrimSpace(userInput)
	low := strings.ToLower(trimmed)

	// 1. 继续执行检测（最高优先级）
	if isContinue(low) {
		return Result{Intent: IntentContinue, CleanInput: trimmed, Confidence: 1.0, Source: "keyword"}
	}

	// 2. QA 问答检测
	if isQA(low) {
		return Result{Intent: IntentQA, CleanInput: trimmed, Confidence: 0.9, Source: "keyword"}
	}

	// 3. Explain 解释检测
	if isExplain(low) {
		return Result{Intent: IntentExplain, CleanInput: trimmed, Confidence: 0.9, Source: "keyword"}
	}

	// 4. Review 审查检测
	if isReview(low) {
		return Result{Intent: IntentReview, CleanInput: trimmed, Confidence: 0.9, Source: "keyword"}
	}

	// 5. Explore 探索检测
	if isExplore(low) {
		return Result{Intent: IntentExplore, CleanInput: trimmed, Confidence: 0.85, Source: "keyword"}
	}

	// 6. Deep 前缀检测
	if deepagent.LooksDeep(trimmed) {
		clean := deepagent.StripPrefix(trimmed)
		return Result{Intent: IntentDeep, CleanInput: clean, Confidence: 1.0, Source: "prefix"}
	}

	// 7. Team 前缀检测
	if looksMulti(low) {
		clean := stripMultiPrefix(trimmed)
		return Result{Intent: IntentTeam, CleanInput: clean, Confidence: 1.0, Source: "prefix"}
	}

	// 8. LLM 语义兜底：识别规则覆盖不到的长尾自然语言
	if c.llm != nil {
		if it, conf := c.classifyLLM(trimmed); it != IntentNormal && conf >= 0.7 {
			return Result{Intent: it, CleanInput: trimmed, Confidence: conf, Source: "llm"}
		}
	}

	// 9. 默认：普通对话
	return Result{Intent: IntentNormal, CleanInput: trimmed, Confidence: 0.5, Source: "default"}
}

const intentSystemPrompt = `Classify the user's intent into ONE of:
- "deep": a complex multi-step coding/implementation task needing plan→act→reflect
- "team": a task that benefits from parallel multi-agent roles (research/explore in parallel)
- "qa": a simple question or answer request, quick fact lookup
- "explain": code explanation, architecture walkthrough, or concept breakdown
- "review": code review, bug analysis, or quality assessment
- "explore": codebase exploration, finding files/functions, understanding structure
- "normal": ordinary single-turn conversation or a simple task

Reply with STRICT JSON: {"intent":"<deep|team|qa|explain|review|explore|normal>"}`

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
	case "qa":
		return IntentQA, 0.85
	case "explain":
		return IntentExplain, 0.85
	case "review":
		return IntentReview, 0.8
	case "explore":
		return IntentExplore, 0.8
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

func isQA(low string) bool {
	qaSignals := []string{"是什么意思", "请问", "能否", "可以问", "why does", "how to", "what is the", "what's"}
	hasQuestion := strings.HasSuffix(strings.TrimSpace(low), "?") || strings.HasSuffix(strings.TrimSpace(low), "？")
	if hasQuestion && len(low) < 30 {
		return true
	}
	for _, s := range qaSignals {
		if strings.Contains(low, s) && len(low) < 50 {
			return true
		}
	}
	return false
}

func isExplain(low string) bool {
	explainSignals := []string{"解释", "说明", "讲解", "分析", "explain", "explain", "walk through", "怎么工作", "how does", "原理", "机制"}
	for _, s := range explainSignals {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

func isReview(low string) bool {
	reviewSignals := []string{"审查", "review", "检查代码", "代码检查", "代码审计", "code review", "检查bug", "优化建议", "重构建议"}
	for _, s := range reviewSignals {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

func isExplore(low string) bool {
	exploreSignals := []string{"找到", "查找", "搜索", "搜索文件", "搜索函数", "find", "search", "locate", "结构", "架构", "目录结构", "代码结构"}
	for _, s := range exploreSignals {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
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

// corefPatterns 将中文/英文指代映射到 EntityContext 字段。
// 仅在对应字段非空时才替换，避免凭空捏造实体。
var corefPatterns = []struct {
	field string
	words []string
}{
	{"ssh", []string{"那台机器", "那台服务器", "这台机器", "这台服务器", "那个服务器", "那台", "这台", "that server", "the same server", "that machine"}},
	{"file", []string{"刚才那个文件", "刚才的文件", "那个文件", "这个文件"}},
	{"dir", []string{"刚才那个目录", "刚才的目录", "那个目录", "这个目录"}},
	{"session", []string{"那个会话", "当前会话", "刚才的会话", "那个终端"}},
}

func entityValue(ec *EntityContext, field string) string {
	switch field {
	case "ssh":
		return ec.LastSSHConnection
	case "file":
		return ec.LastFile
	case "dir":
		return ec.LastDir
	case "session":
		return ec.LastSessionID
	}
	return ""
}

// resolveCoreference 把输入中的代词替换为最近实体，返回 (解析后文本, 是否发生解析)。
func resolveCoreference(input string, ec *EntityContext) (string, bool) {
	out := input
	hit := false
	for _, p := range corefPatterns {
		val := entityValue(ec, p.field)
		if val == "" {
			continue
		}
		for _, w := range p.words {
			if strings.Contains(out, w) {
				out = strings.Replace(out, w, val, 1)
				hit = true
			}
		}
	}
	return out, hit
}

var (
	reConn = regexp.MustCompile(`"connection"\s*:\s*"([^"]+)"`)
	rePath = regexp.MustCompile(`"path"\s*:\s*"([^"]+)"`)
	reDir  = regexp.MustCompile(`"(?:dir|directory)"\s*:\s*"([^"]+)"`)
	reSess = regexp.MustCompile(`"session_id"\s*:\s*"([^"]+)"`)
)

// ExtractEntities 从近期消息内容（按时间正序）中提取最近被引用/使用的实体。
// 取每种实体的"最后一次出现"，用于跨轮指代消解。无实体时返回 nil。
func ExtractEntities(contents []string) *EntityContext {
	ec := &EntityContext{}
	found := false
	for _, c := range contents {
		if m := reConn.FindStringSubmatch(c); m != nil {
			ec.LastSSHConnection, found = m[1], true
		}
		if m := rePath.FindStringSubmatch(c); m != nil {
			ec.LastFile, found = m[1], true
		}
		if m := reDir.FindStringSubmatch(c); m != nil {
			ec.LastDir, found = m[1], true
		}
		if m := reSess.FindStringSubmatch(c); m != nil {
			ec.LastSessionID, found = m[1], true
		}
	}
	if !found {
		return nil
	}
	return ec
}
