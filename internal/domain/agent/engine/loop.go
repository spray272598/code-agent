package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/agent/plan"
	"github.com/spray272598/code-agent/internal/domain/audit"
	"github.com/spray272598/code-agent/internal/domain/blob"
	"github.com/spray272598/code-agent/internal/domain/contextx"
	"github.com/spray272598/code-agent/internal/domain/hook"
	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
	"github.com/spray272598/code-agent/internal/domain/skill"
	"github.com/spray272598/code-agent/internal/domain/subagent"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/domain/workflow"
)

// maxToolResultChars aliases the package constant for local call sites.
const maxToolResultChars = MaxToolResultChars

type Loop struct {
	llm          port.ILLMPort
	tools        *tool.MapRegistry
	sessions     sessrepo.ISessionRepository
	messages     sessrepo.IMessageRepository
	summaries    sessrepo.ISummaryRepository
	perm         *security.Guard
	compressor   *contextx.Compressor
	skills       *skill.Service
	hooks        *hook.Bus
	specSvc      SpecService
	memSvc       *memory.Service
	memCtx       *coding.MemoryContext
	audit        audit.Repository
	subRunner    *subagent.Runner
	blobs        blob.Store
	blobThresh   int
	toolCache    *tool.ResultCache
	tokens       *TokenManager
	histLoader   *HistoryLoader
	maxRounds    int
	tokenBudget  int
	systemPrompt string
	// system prompt cache (tools + skill id)
	sysCacheKey      string
	sysCacheVal      string
	skillInterceptor *skill.BlockInterceptor
	workflowBridge   *workflow.LoopBridge
}

func NewLoop(
	llm port.ILLMPort,
	tools *tool.MapRegistry,
	sessions sessrepo.ISessionRepository,
	messages sessrepo.IMessageRepository,
	perm *security.Guard,
	maxRounds, tokenBudget int,
) *Loop {
	if maxRounds <= 0 {
		maxRounds = DefaultMaxRounds
	}
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	comp := contextx.NewCompressor(tokenBudget / 2)
	summarizer := contextx.NewSummarizer(llm)
	comp.SetSummarizer(summarizer)
	return &Loop{
		llm: llm, tools: tools, sessions: sessions, messages: messages,
		perm: perm, compressor: comp,
		maxRounds: maxRounds, tokenBudget: tokenBudget,
		systemPrompt: defaultSystem(),
		toolCache:    tool.NewResultCache(30*time.Second, 128),
		tokens:       NewTokenManager(tokenBudget),
		histLoader:   NewHistoryLoader(messages, comp),
	}
}

// NewLoopWithSummarizer creates a Loop with a custom summarizer.
// Use this in tests to avoid consuming the main LLM queue.
func NewLoopWithSummarizer(
	llm port.ILLMPort,
	tools *tool.MapRegistry,
	sessions sessrepo.ISessionRepository,
	messages sessrepo.IMessageRepository,
	perm *security.Guard,
	maxRounds, tokenBudget int,
	summarizer *contextx.Summarizer,
) *Loop {
	loop := NewLoop(llm, tools, sessions, messages, perm, maxRounds, tokenBudget)
	if summarizer != nil {
		loop.compressor.SetSummarizer(summarizer)
	}
	return loop
}

func (l *Loop) SetSkills(s *skill.Service)   { l.skills = s }
func (l *Loop) SetHooks(h *hook.Bus)         { l.hooks = h }
func (l *Loop) SetSpecService(s SpecService) { l.specSvc = s }
func (l *Loop) SetMemory(svc *memory.Service, mc *coding.MemoryContext) {
	l.memSvc = svc
	l.memCtx = mc
}

func (l *Loop) SetSkillInterceptor(interceptor *skill.BlockInterceptor) {
	l.skillInterceptor = interceptor
}
func (l *Loop) SetWorkflowBridge(bridge *workflow.LoopBridge) { l.workflowBridge = bridge }
func (l *Loop) SetAudit(a audit.Repository)                   { l.audit = a }
func (l *Loop) SetSummaryRepo(s sessrepo.ISummaryRepository)  { l.summaries = s }
func (l *Loop) SetSubRunner(r *subagent.Runner) {
	l.subRunner = r
	// Wire window isolation (M5.7-4): subagent results are distilled into a
	// short summary before being written back into the parent context, so long
	// subagent transcripts never balloon the main window.
	if r != nil && l.compressor != nil && l.compressor.Summarizer != nil {
		sum := l.compressor.Summarizer
		r.SummarizeResult = func(ctx context.Context, role, output string) (string, error) {
			// Explore/verify roles rarely need the full transcript; summarize
			// aggressively. General role keeps a bit more detail.
			roleHint := "Distill this subagent result into a concise summary for the parent agent: key findings, files touched, decisions, and any errors. No raw tool output."
			if strings.EqualFold(role, "general") {
				roleHint = "Summarize this subagent result for the parent agent: what was accomplished, files changed, remaining issues. Keep concrete paths/decisions, drop raw logs."
			}
			maps := []map[string]any{{"role": "user", "content": roleHint + "\n\n---\n" + output}}
			return sum.Summarize(ctx, "", maps)
		}
	}
}

func (l *Loop) SetBlobStore(s blob.Store, threshold int) {
	l.blobs = s
	if threshold <= 0 {
		threshold = blob.DefaultThreshold
	}
	l.blobThresh = threshold
}

func (l *Loop) maybeOffload(ctx context.Context, sessionID, toolName, resText string) string {
	if l.blobs == nil {
		return budget(resText)
	}
	or := blob.OffloadIfLarge(ctx, l.blobs, sessionID, toolName, resText, l.blobThresh)
	if or.Offloaded {
		telemetry.IncBlobOffload()
		telemetry.TraceEvent(map[string]any{
			"event": "blob_offload", "session": sessionID, "tool": toolName, "bytes": or.Bytes, "key": or.ObjectKey,
		})
		return or.Preview
	}
	return budget(resText)
}

// SpecService is the minimal spec service interface used by the engine.
// Implemented by spec.Service in domain/spec.
type SpecService interface {
	PromptSection() string
	HasSpec() bool
	HasCLAUDE() bool
	Reload() error
	Progress() float64
	Summary() string
	// plan.SpecData compatibility
	HasContent() bool
	GetTitle() string
	GetGoal() string
	GetTasks() []plan.TaskData
	GetChecklist() []plan.ChecklistData
	GetConstraints() []string
	GetAcceptance() []string
}

type RunOptions struct {
	AutoApprove  bool
	ForceCompact bool
	// ControlCh delivers mid-run instructions (replan / pause / interrupt).
	// Optional; when nil the loop runs uninterrupted.
	ControlCh <-chan Control
}

// replanFailStreak is the consecutive tool-failure count that triggers an
// automatic interruptible re-plan.
const replanFailStreak = 3

func (l *Loop) reflect(ctx context.Context, problem, detail string) string {
	if l.llm == nil {
		return "Tool failed; try a different path or tool."
	}
	resp, err := l.llm.Generate(ctx, &port.ChatRequest{
		SystemPrompt: "You are a coding agent reflector. Given a failure, output 2-4 short bullets: root cause, next action, which tool to use. No JSON tool calls.",
		Messages: []port.ChatMessage{{
			Role: "user", Content: "Problem: " + problem + "\nDetail: " + detail,
		}},
		Temperature: DefaultTemperature,
		MaxTokens:   ReflectMaxTokens,
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "Failure analysis: check path/args; try glob/grep to locate targets; avoid repeating the same call."
	}
	return strings.TrimSpace(resp.Content)
}

func (l *Loop) execTool(ctx context.Context, name string, args map[string]any) (string, string) {
	t := l.tools.Get(name)
	if t == nil {
		return fmt.Sprintf("tool not found: %s", name), "tool"
	}
	r, err := t.Execute(ctx, args)
	if err != nil {
		return err.Error(), "tool"
	}
	if r.IsError {
		return r.Text, "tool"
	}
	return r.Text, ""
}

func (l *Loop) Permission() *security.Guard { return l.perm }
