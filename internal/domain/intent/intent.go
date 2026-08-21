package intent

// Intent 意图类型
type Intent int

const (
	IntentNormal   Intent = iota // 普通对话（单Agent ReAct）
	IntentDeep                   // 深度模式（Plan→Act→Reflect）
	IntentTeam                   // 团队并行模式
	IntentContinue               // 继续执行（恢复中断）
)

// String 返回意图名称
func (i Intent) String() string {
	switch i {
	case IntentDeep:
		return "deep"
	case IntentTeam:
		return "team"
	case IntentContinue:
		return "continue"
	default:
		return "normal"
	}
}

// Result 意图识别结果
type Result struct {
	Intent     Intent
	CleanInput string  // 去除路由前缀后的输入
	Confidence float64 // 置信度 0-1
	Source     string  // 识别来源（"prefix"、"keyword"、"default"）
}

// Router 意图路由接口
type Router interface {
	Classify(userInput string) Result
	// ClassifyWithContext 在 Classify 基础上做跨轮指代消解（ec 为 nil 等价于 Classify）
	ClassifyWithContext(userInput string, ec *EntityContext) Result
}
