package skill

import (
	"context"
	"strings"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

// ITool is an alias of tool.ITool for convenience within the skill package.
// Re-exported so callers can use BuildSkillTools without an extra import.
type ITool = tool.ITool

// Ensure ExecutableSkill satisfies tool.ITool at compile time.
var _ tool.ITool = (*ExecutableSkill)(nil)

// SkillInfo formats a skill for prompt injection (system prompt). Returns an
// XML-style envelope modeled after grok-build-main's build_skill_message.
func SkillInfo(sk *Skill) string {
	if sk == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<skill name=\"")
	b.WriteString(sk.Name)
	b.WriteString("\"")
	if sk.Description != "" {
		b.WriteString(" description=\"")
		b.WriteString(escapeAttr(sk.Description))
		b.WriteString("\"")
	}
	if sk.Path != "" {
		b.WriteString(" path=\"")
		b.WriteString(escapeAttr(sk.Path))
		b.WriteString("\"")
	}
	b.WriteString(">\n")
	b.WriteString(sk.Body)
	b.WriteString("\n</skill>")
	return b.String()
}

// SkillsBlock formats multiple skills into a single prompt block with an
// index listing each skill's name and path.
func SkillsBlock(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<skill_information>\n")

	// <skills_referenced> index with deduplication.
	b.WriteString("<skills_referenced>\n")
	seen := map[string]bool{}
	for _, sk := range skills {
		if sk == nil {
			continue
		}
		key := sk.Name + "|" + sk.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		b.WriteString("<skill name=\"")
		b.WriteString(sk.Name)
		b.WriteString("\" path=\"")
		b.WriteString(escapeAttr(sk.Path))
		b.WriteString("\"/>\n")
	}
	b.WriteString("</skills_referenced>\n")

	for _, sk := range skills {
		if sk == nil {
			continue
		}
		b.WriteString(SkillInfo(sk))
		b.WriteString("\n")
	}
	b.WriteString("</skill_information>")
	return b.String()
}

// escapeAttr performs minimal XML attribute escaping for skill metadata
// fields that are inserted into the generated <skill> tag.
func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// Execute runs a skill with the given arguments and returns the substituted
// body. This is the programmatic entry point for "Skill可执行".
func (s *Service) Execute(ctx context.Context, id, args string) (string, error) {
	sk := s.Get(id)
	if sk == nil {
		return "", ErrSkillNotFound
	}
	composed, cycle := s.ComposeWithCycleCheck(sk)
	if cycle {
		return "", ErrSkillCycle
	}
	var b strings.Builder
	for _, c := range composed {
		if c.Body == "" {
			continue
		}
		substituted := Substitute(c.Body, args, SubstitutionContext{
			SkillDir:  c.Path,
			SessionID: sessionIDFromCtx(ctx),
		})
		b.WriteString("<skill name=\"")
		b.WriteString(c.Name)
		b.WriteString("\">\n")
		b.WriteString(substituted)
		b.WriteString("\n</skill>\n\n")
	}
	return b.String(), nil
}
