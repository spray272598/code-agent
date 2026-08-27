package einoorch

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/memory"
	"github.com/spray272598/code-agent/internal/domain/skill"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
)

type SpecProvider interface {
	PromptSection() string
}

type AgentsMdProvider interface {
	Discover(workspace string) []AgentsMdFile
}

type DefaultAgentsMdProvider struct{}

func (DefaultAgentsMdProvider) Discover(workspace string) []AgentsMdFile {
	return DiscoverAgentsMdFiles(workspace, 4)
}

type PromptBuilder struct {
	mu       sync.RWMutex
	tools    *domtool.MapRegistry
	skills   *skill.Service
	mem      *memory.Service
	spec     SpecProvider
	agentsMd AgentsMdProvider
	ctx      *PromptContext

	cacheKey string
	cacheVal string
}

func NewPromptBuilder(ctx *PromptContext, tools *domtool.MapRegistry) *PromptBuilder {
	if ctx == nil {
		ctx = NewPromptContext()
	}
	return &PromptBuilder{
		tools:    tools,
		ctx:      ctx,
		agentsMd: DefaultAgentsMdProvider{},
	}
}

func (p *PromptBuilder) SetSkills(s *skill.Service)             { p.skills = s }
func (p *PromptBuilder) SetMemory(m *memory.Service)            { p.mem = m }
func (p *PromptBuilder) SetSpecService(s SpecProvider)          { p.spec = s }
func (p *PromptBuilder) SetAgentsMdProvider(a AgentsMdProvider) { p.agentsMd = a }
func (p *PromptBuilder) SetContext(ctx *PromptContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctx = ctx
	p.cacheKey = ""
	p.cacheVal = ""
}

func (p *PromptBuilder) Context() *PromptContext { return p.ctx }

func (p *PromptBuilder) Build(ctx context.Context, userID, projectID, userInput string, activeSkill *skill.Skill, tokenBudget int) string {
	if p == nil {
		return p.ctx.Header()
	}

	catalog := BuildToolCatalog(p.tools)
	staticKey := fmt.Sprintf("%s|%s|%d|%d|%t",
		toolsFingerprint(p.tools),
		skillID(activeSkill),
		tokenBudget,
		p.ctx.Audience,
		p.ctx.IncludesBrowser,
	)

	p.mu.RLock()
	hit := staticKey == p.cacheKey && p.cacheVal != ""
	static := p.cacheVal
	p.mu.RUnlock()

	if !hit {
		static = p.buildStatic(catalog, activeSkill, tokenBudget)
		p.mu.Lock()
		p.cacheKey = staticKey
		p.cacheVal = static
		p.mu.Unlock()
	}

	var dyn []string
	if p.mem != nil && userID != "" {
		if block := p.mem.FormatForPrompt(ctx, userID, projectID, userInput, 8); block != "" {
			dyn = append(dyn, block)
		}
	}
	if p.spec != nil {
		if sec := p.spec.PromptSection(); sec != "" {
			dyn = append(dyn, sec)
		}
	}

	if len(dyn) > 0 {
		return static + "\n" + strings.Join(dyn, "\n")
	}
	return static
}

func (p *PromptBuilder) buildStatic(catalog ToolCatalog, activeSkill *skill.Skill, tokenBudget int) string {
	var b strings.Builder

	b.WriteString(p.ctx.Header())

	if p.ctx.Audience == AudiencePrimary {
		b.WriteString("\n")
		b.WriteString(WorkPolicySection())

		b.WriteString("\n")
		b.WriteString(SandboxPolicySection())

		b.WriteString("\n")
		b.WriteString(catalog.ToolCallingSection())
		b.WriteString(catalog.BackgroundTasksSection())
		b.WriteString(catalog.TaskManagementSection())
		b.WriteString(catalog.SkillSection())

		b.WriteString("\n")
		b.WriteString(CommunicationSection())

		b.WriteString("\n")
		b.WriteString(FormattingSection())

		if catalog.HasEdit {
			b.WriteString("\n")
			b.WriteString(CodeChangeRulesSection())
		}

		if catalog.HasExec || catalog.HasEdit {
			b.WriteString("\n")
			b.WriteString(TestingDisciplineSection())
		}

		b.WriteString("\n")
		b.WriteString(SubagentCatalog())

		b.WriteString("\n")
		b.WriteString(DelegationGuidanceSection())

		if !p.ctx.IsNonInteractive {
			b.WriteString("\n")
			b.WriteString(UserGuideSection())
		}

		if p.ctx.IncludesBrowser {
			b.WriteString("\n")
			b.WriteString(BrowserVerificationSection())
		}

		if p.ctx.MemoryEnabled {
			b.WriteString("\n")
			b.WriteString(MemorySection(true))
		}
	} else {
		b.WriteString("\n")
		b.WriteString(SubagentCatalog())
	}

	b.WriteString("\n")
	b.WriteString(p.ctx.UserInfoBlock())

	if activeSkill != nil {
		b.WriteString("\n")
		b.WriteString(p.skills.PromptSection(activeSkill))
		if len(activeSkill.Tools) > 0 {
			b.WriteString("\nYou may ONLY use these tools while this skill is active: ")
			b.WriteString(strings.Join(activeSkill.Tools, ", "))
			b.WriteString("\n")
		}
	}

	if tokenBudget > 0 {
		b.WriteString(fmt.Sprintf("\n## Token budget\nStay within ~%d tokens of context. Prefer concise tool results and final answers.\n", tokenBudget))
	}

	return b.String()
}

func (p *PromptBuilder) BuildCompact(tokenBudget int) string {
	catalog := BuildToolCatalog(p.tools)
	var b strings.Builder
	b.WriteString("You are continuing a previous conversation. The context has been summarized to save tokens.\n\n")
	b.WriteString("Key system guidance:\n")
	b.WriteString("- Use specialized tools (")
	b.WriteString(catalog.ReadName)
	b.WriteString(", ")
	b.WriteString(catalog.EditName)
	b.WriteString(") for file operations.\n")
	b.WriteString("- Prefer concise communication. Lead with the answer.\n")
	b.WriteString("- Run tests after changes. Do not claim completion without verification.\n")
	b.WriteString("\n")
	b.WriteString("Continue working on the user's original request. Pick up where the work left off.\n")
	if tokenBudget > 0 {
		b.WriteString(fmt.Sprintf("\nToken budget: ~%d tokens.\n", tokenBudget))
	}
	return b.String()
}

func (p *PromptBuilder) MessageModifier(ctx context.Context, userID, projectID, userInput string, sk *skill.Skill, budget int) func(context.Context, []*schema.Message) []*schema.Message {
	sys := p.Build(ctx, userID, projectID, userInput, sk, budget)
	return func(_ context.Context, input []*schema.Message) []*schema.Message {
		out := make([]*schema.Message, 0, len(input)+1)
		out = append(out, schema.SystemMessage(sys))
		for _, m := range input {
			if m != nil && m.Role == schema.System {
				continue
			}
			out = append(out, m)
		}
		return out
	}
}

func (p *PromptBuilder) CompactMessageModifier(ctx context.Context, budget int) func(context.Context, []*schema.Message) []*schema.Message {
	sys := p.BuildCompact(budget)
	return func(_ context.Context, input []*schema.Message) []*schema.Message {
		out := make([]*schema.Message, 0, len(input)+1)
		out = append(out, schema.SystemMessage(sys))
		for _, m := range input {
			if m != nil && m.Role == schema.System {
				continue
			}
			out = append(out, m)
		}
		return out
	}
}

func (p *PromptBuilder) BuildForSubagent(role string, workspace string, tools []string, maxSteps int) string {
	var b strings.Builder
	b.WriteString(SubagentPrompt(role))

	if len(tools) > 0 {
		b.WriteString("\n<available_tools>\n")
		b.WriteString("You have access to these tools in this session:\n")
		for _, t := range tools {
			b.WriteString("- `")
			b.WriteString(t)
			b.WriteString("`\n")
		}
		b.WriteString("</available_tools>\n")
	}

	if maxSteps > 0 {
		b.WriteString(fmt.Sprintf("\n<step_limit>\nMaximum steps for this subagent: %d. If you cannot complete within the limit, return your partial progress and the remaining work.\n</step_limit>\n", maxSteps))
	}

	if workspace != "" {
		b.WriteString("\n<workspace>\nYour workspace is: ")
		b.WriteString(workspace)
		b.WriteString("\n</workspace>\n")
	}

	b.WriteString("\n")
	b.WriteString(p.ctx.UserInfoBlock())

	return b.String()
}

func toolsFingerprint(reg *domtool.MapRegistry) string {
	if reg == nil {
		return ""
	}
	var b strings.Builder
	for _, t := range reg.Descriptions() {
		b.WriteString(t["name"])
		b.WriteByte(',')
	}
	return b.String()
}

func skillID(sk *skill.Skill) string {
	if sk != nil {
		return sk.ID
	}
	return ""
}

func NewPromptBuilderWithDefaults() *PromptBuilder {
	return NewPromptBuilder(NewPromptContext(), nil)
}
