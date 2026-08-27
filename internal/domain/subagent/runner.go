package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/security"
	"github.com/spray272598/code-agent/internal/domain/telemetry"
	"github.com/spray272598/code-agent/internal/domain/tool"
	"github.com/spray272598/code-agent/internal/domain/tool/coding"
	"github.com/spray272598/code-agent/internal/types/common"
)

// ProgressFunc reports subagent lifecycle to the parent (SSE).
type ProgressFunc func(ev Progress)

type Progress struct {
	ID      string `json:"id"`
	Role    string `json:"role,omitempty"`
	Status  string `json:"status"` // start|tool|done|error
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Spec one subagent task.
type Spec struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	Role     string   `json:"role"` // explore|verify|general|custom
	Tools    []string `json:"tools,omitempty"`
	MaxSteps int      `json:"maxSteps,omitempty"`
	// Isolation: "" | "worktree" | "process"
	// worktree = git worktree filesystem isolation
	// process  = run bash via separate OS process group (crash isolation)
	Isolation string `json:"isolation,omitempty"`
}

// Result of one subagent.
type Result struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Status string `json:"status"` // ok|error
	Output string `json:"output"`
	// Summary is the window-isolated distillation of Output (M5.7-4). When set,
	// the parent context receives Summary instead of the full Output, so long
	// subagent transcripts never balloon the main window.
	Summary  string `json:"summary,omitempty"`
	Steps    int    `json:"steps"`
	Tokens   int    `json:"tokens"`
	WorkDir  string `json:"workDir,omitempty"`
	Duration int64  `json:"durationMs"`
}

// RoleConfig tool allowlist + step limit.
type RoleConfig struct {
	Tools    []string
	MaxSteps int
}

// Runner executes subagents with limited tools and optional concurrency.
type Runner struct {
	LLM           port.ILLMPort
	ParentTools   *tool.MapRegistry
	BaseWorkspace string
	MaxConcurrent int
	DefaultSteps  int
	Roles         map[string]RoleConfig
	Worktrees     WorktreePort
	OnProgress    ProgressFunc
	// forbid nested delegate
	DenyTools map[string]bool
	// SummarizeResult optionally distills a subagent's full transcript into a
	// short window-friendly summary before it is written back into the parent
	// context (M5.7-4). When nil, the raw Output is used (legacy behaviour).
	SummarizeResult func(ctx context.Context, role, output string) (summary string, err error)
	// GuardFactory creates an isolated Guard for each subagent. When nil,
	// subagents share the parent's security context (legacy behaviour).
	GuardFactory func(workspace string) *security.Guard
}

// WorktreePort isolation (implemented in worktree package).
type WorktreePort interface {
	Create(id string) (path string, cleanup func(), err error)
}

func NewRunner(llm port.ILLMPort, tools *tool.MapRegistry, workspace string) *Runner {
	return &Runner{
		LLM: llm, ParentTools: tools, BaseWorkspace: workspace,
		MaxConcurrent: 3, DefaultSteps: 8,
		Roles:     DefaultRoles(),
		DenyTools: map[string]bool{"delegate": true},
	}
}

func DefaultRoles() map[string]RoleConfig {
	return map[string]RoleConfig{
		"explore": {
			Tools: []string{"read_file", "glob", "grep"}, MaxSteps: 8,
		},
		"verify": {
			Tools: []string{"bash", "read_file", "grep", "glob"}, MaxSteps: 6,
		},
		"general": {
			Tools: []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep"}, MaxSteps: 10,
		},
	}
}

// RunAll executes specs sequentially or in parallel (up to MaxConcurrent).
func (r *Runner) RunAll(ctx context.Context, specs []Spec) []Result {
	if len(specs) == 0 {
		return nil
	}
	n := r.MaxConcurrent
	if n <= 0 {
		n = 1
	}
	if n > len(specs) {
		n = len(specs)
	}
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	out := make([]Result, len(specs))
	for i := range specs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = r.RunOne(ctx, specs[i])
		}(i)
	}
	wg.Wait()
	return out
}

func (r *Runner) RunOne(ctx context.Context, spec Spec) Result {
	start := time.Now()
	if spec.ID == "" {
		spec.ID = fmt.Sprintf("sa-%d", time.Now().UnixNano()%1e9)
	}
	if spec.Role == "" {
		spec.Role = "general"
	}
	r.emit(Progress{ID: spec.ID, Role: spec.Role, Status: "start", Message: truncate(spec.Prompt, 120)})

	workDir := r.BaseWorkspace
	var cleanup func()
	if strings.EqualFold(spec.Isolation, "worktree") && r.Worktrees != nil {
		path, c, err := r.Worktrees.Create(spec.ID)
		if err != nil {
			// fallback to base
			r.emit(Progress{ID: spec.ID, Status: "error", Message: "worktree: " + err.Error() + "; using base workspace"})
		} else {
			workDir = path
			cleanup = c
		}
	}
	if cleanup != nil {
		defer cleanup()
	}

	reg := r.buildRegistry(spec, workDir)
	// Create isolated Guard for this subagent's workspace
	var guard *security.Guard
	if r.GuardFactory != nil {
		guard = r.GuardFactory(workDir)
	}

	maxSteps := spec.MaxSteps
	if maxSteps <= 0 {
		if rc, ok := r.Roles[strings.ToLower(spec.Role)]; ok && rc.MaxSteps > 0 {
			maxSteps = rc.MaxSteps
		} else {
			maxSteps = r.DefaultSteps
		}
	}

	output, steps, tokens, err := r.miniLoop(ctx, spec, reg, maxSteps, guard)
	res := Result{
		ID: spec.ID, Role: spec.Role, Output: output, Steps: steps, Tokens: tokens,
		WorkDir: workDir, Duration: time.Since(start).Milliseconds(), Status: "ok",
	}
	// Window isolation (M5.7-4): distill the transcript into a short summary so
	// the parent context never ingests the full subagent output verbatim.
	if r.SummarizeResult != nil && output != "" {
		if s, serr := r.SummarizeResult(ctx, spec.Role, output); serr == nil && s != "" {
			res.Summary = s
		}
	}
	if err != nil {
		res.Status = "error"
		if res.Output == "" {
			res.Output = err.Error()
		} else {
			res.Output = res.Output + "\n[error] " + err.Error()
		}
		r.emit(Progress{ID: spec.ID, Role: spec.Role, Status: "error", Message: res.Output})
	} else {
		r.emit(Progress{ID: spec.ID, Role: spec.Role, Status: "done", Message: truncate(output, 200)})
	}
	return res
}

func (r *Runner) buildRegistry(spec Spec, workDir string) *tool.MapRegistry {
	allow := map[string]bool{}
	role := strings.ToLower(spec.Role)
	if rc, ok := r.Roles[role]; ok {
		for _, t := range rc.Tools {
			allow[t] = true
		}
	}
	for _, t := range spec.Tools {
		allow[t] = true
	}
	if len(allow) == 0 {
		// default general
		for _, t := range DefaultRoles()["general"].Tools {
			allow[t] = true
		}
	}

	// tools bound to this workDir
	ws := coding.NewWorkspace(workDir)
	local := tool.NewRegistry()
	bash := coding.NewBash(ws, 45)
	if strings.EqualFold(spec.Isolation, "process") || strings.EqualFold(spec.Isolation, "worktree") {
		// process-level isolation for shell (worktree also gets isolated bash)
		bash = coding.NewBashIsolated(ws, 45)
	}
	candidates := []tool.ITool{
		coding.NewReadFile(ws), coding.NewWriteFile(ws), coding.NewEditFile(ws),
		bash, coding.NewGlob(ws), coding.NewGrep(ws),
	}
	// also allow parent tools that are not fs-bound if allowlisted (e.g. memory_search)
	if r.ParentTools != nil {
		for _, t := range r.ParentTools.List() {
			name := t.Name()
			if r.DenyTools[name] {
				continue
			}
			if !allow[name] {
				continue
			}
			// prefer local coding tools for file ops
			if name == "read_file" || name == "write_file" || name == "edit_file" ||
				name == "bash" || name == "glob" || name == "grep" {
				continue
			}
			local.Register(t)
		}
	}
	for _, t := range candidates {
		if allow[t.Name()] {
			local.Register(t)
		}
	}
	return local
}

func (r *Runner) miniLoop(ctx context.Context, spec Spec, reg *tool.MapRegistry, maxSteps int, guard *security.Guard) (string, int, int, error) {
	if r.LLM == nil {
		return "", 0, 0, fmt.Errorf("llm unavailable")
	}
	sys := fmt.Sprintf(`You are a Code-Agent SubAgent (role=%s).
Complete the task with available tools. Reply with JSON tool calls only when needed:
{"name":"tool","args":{...}}
When finished, answer in plain text (no JSON).
Do not invent tools. Stay concise.`, spec.Role)
	descs := reg.Descriptions()
	sys += "\n## Tools\n"
	for _, d := range descs {
		sys += fmt.Sprintf("- %s: %s\n", d["name"], d["description"])
	}

	messages := []port.ChatMessage{{Role: "user", Content: spec.Prompt}}
	tokens, steps := 0, 0
	for step := 1; step <= maxSteps; step++ {
		select {
		case <-ctx.Done():
			return "", steps, tokens, ctx.Err()
		default:
		}
		t0 := time.Now()
		resp, err := r.LLM.Generate(ctx, &port.ChatRequest{
			SystemPrompt: sys, Messages: messages, Temperature: 0.2,
		})
		telemetry.ObserveLLM(time.Since(t0))
		if err != nil {
			return "", steps, tokens, err
		}
		if resp.TotalTokens > 0 {
			tokens += resp.TotalTokens
		} else {
			tokens += common.EstimateTokens(resp.Content)
		}
		calls := parseToolCalls(resp.Content)
		if len(calls) == 0 {
			out := strings.TrimSpace(resp.Content)
			if out == "" {
				out = "(empty)"
			}
			return out, steps, tokens, nil
		}
		messages = append(messages, port.ChatMessage{Role: "assistant", Content: resp.Content})
		for _, tc := range calls {
			steps++
			telemetry.IncToolCall()
			r.emit(Progress{ID: spec.ID, Role: spec.Role, Status: "tool", Message: tc.Name, Data: tc.Args})
			t := reg.Get(tc.Name)
			var text string
			if t == nil {
				text = "tool not found: " + tc.Name
			} else {
				// Check isolated Guard before execution
				if guard != nil {
					if decision := guard.Check(spec.ID, tc.Name, tc.Args); decision.Action == security.ActionDeny {
						text = "denied by sandbox: " + decision.Reason
						messages = append(messages, port.ChatMessage{
							Role: "tool", Content: text, Name: tc.Name, ToolCallID: "sa-" + tc.Name,
						})
						continue
					}
				}
				tt := time.Now()
				res, err := t.Execute(ctx, tc.Args)
				telemetry.ObserveTool(time.Since(tt))
				if err != nil {
					text = err.Error()
				} else {
					text = res.Text
					if res.IsError {
						text = res.Text
					}
				}
			}
			text = common.TruncateRunes(text, common.SubAgentToolResultMaxRunes)
			messages = append(messages, port.ChatMessage{
				Role: "tool", Content: text, Name: tc.Name, ToolCallID: "sa-" + tc.Name,
			})
		}
		messages = append(messages, port.ChatMessage{Role: "user", Content: "Continue or finish with a plain-text answer."})
	}
	return "subagent hit max steps", steps, tokens, nil
}

func (r *Runner) emit(p Progress) {
	if r != nil && r.OnProgress != nil {
		r.OnProgress(p)
	}
}

func parseToolCalls(response string) []port.ToolCall {
	response = strings.TrimSpace(response)
	if response == "" {
		return nil
	}
	if i := strings.Index(response, "```"); i >= 0 {
		rest := response[i+3:]
		if strings.HasPrefix(strings.ToLower(rest), "json") {
			rest = rest[4:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			response = strings.TrimSpace(rest[:j])
		}
	}
	var single struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(response), &single); err == nil && single.Name != "" {
		if single.Args == nil {
			single.Args = map[string]any{}
		}
		return []port.ToolCall{{Name: single.Name, Args: single.Args}}
	}
	var multi []struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(response), &multi); err == nil {
		var calls []port.ToolCall
		for _, m := range multi {
			if m.Name != "" {
				if m.Args == nil {
					m.Args = map[string]any{}
				}
				calls = append(calls, port.ToolCall{Name: m.Name, Args: m.Args})
			}
		}
		return calls
	}
	if i := strings.Index(response, "{"); i >= 0 {
		if j := strings.LastIndex(response, "}"); j > i {
			return parseToolCalls(response[i : j+1])
		}
	}
	return nil
}

func truncate(s string, n int) string {
	return common.TruncateRunes(s, n)
}

// FormatResults for parent tool observation.
func FormatResults(results []Result) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("SubAgent results (%d):\n", len(results)))
	for _, r := range results {
		body := r.Output
		isolated := ""
		if r.Summary != "" {
			// Window-isolated: parent receives only the distilled summary; the
			// full transcript is intentionally omitted to keep the main context lean.
			body = r.Summary
			isolated = " [window-isolated: summary only, full output omitted]"
		}
		b.WriteString(fmt.Sprintf("\n### [%s] role=%s status=%s steps=%d %dms%s\n%s\n",
			r.ID, r.Role, r.Status, r.Steps, r.Duration, isolated, body))
	}
	return b.String()
}
