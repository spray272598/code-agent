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

// SpecProvider supplies spec-driven content (spec.md/tasks.md/checklist.md/CLAUDE.md)
// for system-prompt injection. Implemented by spec.Service in domain/spec.
type SpecProvider interface {
	PromptSection() string
}

// PromptBuilder builds dynamic system prompts (persona + tools + skill + memory + budget).
type PromptBuilder struct {
	mu       sync.RWMutex
	base     string
	tools    *domtool.MapRegistry
	skills   *skill.Service
	mem      *memory.Service
	spec     SpecProvider
	cacheKey string
	cacheVal string
}

func NewPromptBuilder(base string, tools *domtool.MapRegistry) *PromptBuilder {
	if base == "" {
		base = defaultPersona()
	}
	return &PromptBuilder{base: base, tools: tools}
}

func (p *PromptBuilder) SetSkills(s *skill.Service) { p.skills = s }
func (p *PromptBuilder) SetMemory(m *memory.Service) { p.mem = m }
func (p *PromptBuilder) SetSpecService(s SpecProvider) { p.spec = s }

// Build returns system prompt for this turn (cached when static parts unchanged).
func (p *PromptBuilder) Build(ctx context.Context, userID, projectID, userInput string, activeSkill *skill.Skill, tokenBudget int) string {
	if p == nil {
		return defaultPersona()
	}
	toolsKey := toolsFingerprint(p.tools)
	skillID := ""
	if activeSkill != nil {
		skillID = activeSkill.ID
	}
	// memory is dynamic per query — not fully cached
	staticKey := toolsKey + "|" + skillID + "|" + fmt.Sprint(tokenBudget)

	p.mu.RLock()
	hit := staticKey == p.cacheKey && p.cacheVal != ""
	static := p.cacheVal
	p.mu.RUnlock()

	if !hit {
		var b strings.Builder
		b.WriteString(p.base)
		b.WriteString("\n\n## Available tools\n")
		if p.tools != nil {
			for _, t := range p.tools.Descriptions() {
				b.WriteString("- ")
				b.WriteString(t["name"])
				b.WriteString(": ")
				b.WriteString(t["description"])
				b.WriteString("\n")
			}
		}
		if activeSkill != nil && p.skills != nil {
			b.WriteString("\n")
			b.WriteString(p.skills.PromptSection(activeSkill))
			if len(activeSkill.Tools) > 0 {
				b.WriteString("\nYou may ONLY use these tools while this skill is active: ")
				b.WriteString(strings.Join(activeSkill.Tools, ", "))
				b.WriteString("\n")
			}
		}
		if tokenBudget > 0 {
			b.WriteString(fmt.Sprintf("\n## Token budget\nStay within ~%d tokens of context. Prefer concise tool results and Final answers.\n", tokenBudget))
		}
		static = b.String()
		p.mu.Lock()
		p.cacheKey = staticKey
		p.cacheVal = static
		p.mu.Unlock()
	}

	// dynamic blocks: memory + spec (spec is dynamic so task/checklist progress reflects on next turn)
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

// MessageModifier returns Eino MessageModifier that injects dynamic system prompt each model call.
func (p *PromptBuilder) MessageModifier(ctx context.Context, userID, projectID, userInput string, sk *skill.Skill, budget int) func(context.Context, []*schema.Message) []*schema.Message {
	sys := p.Build(ctx, userID, projectID, userInput, sk, budget)
	return func(_ context.Context, input []*schema.Message) []*schema.Message {
		out := make([]*schema.Message, 0, len(input)+1)
		out = append(out, schema.SystemMessage(sys))
		// drop existing system messages to avoid duplicate
		for _, m := range input {
			if m != nil && m.Role == schema.System {
				continue
			}
			out = append(out, m)
		}
		return out
	}
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
