package einoorch

import (
	"fmt"
	"strings"

	domtool "github.com/spray272598/code-agent/internal/domain/tool"
)

type ToolCatalog struct {
	HasRead   bool
	HasEdit   bool
	HasSearch bool
	HasExec   bool
	HasGlob   bool
	HasPlan   bool
	HasSkill  bool
	HasMCP    bool
	HasWeb    bool

	ReadName   string
	EditName   string
	SearchName string
	ExecName   string
	GlobName   string
	PlanName   string
	SkillName  string
	MCPName    string
	WebName    string
}

func BuildToolCatalog(reg *domtool.MapRegistry) ToolCatalog {
	c := ToolCatalog{}
	if reg == nil {
		return c
	}
	for _, t := range reg.Descriptions() {
		name := t["name"]
		switch {
		case containsAny(name, "read", "file_read", "cat", "head", "tail"):
			if !c.HasRead {
				c.HasRead = true
				c.ReadName = name
			}
		case containsAny(name, "edit", "write", "create", "patch", "apply"):
			if !c.HasEdit {
				c.HasEdit = true
				c.EditName = name
			}
		case containsAny(name, "grep", "search", "find", "search_code", "code_search"):
			if !c.HasSearch {
				c.HasSearch = true
				c.SearchName = name
			}
		case containsAny(name, "bash", "shell", "exec", "terminal", "run", "cmd"):
			if !c.HasExec {
				c.HasExec = true
				c.ExecName = name
			}
		case containsAny(name, "list", "glob", "ls", "dir", "tree"):
			if !c.HasGlob {
				c.HasGlob = true
				c.GlobName = name
			}
		case containsAny(name, "plan", "todo", "task_list", "todo_write"):
			if !c.HasPlan {
				c.HasPlan = true
				c.PlanName = name
			}
		case containsAny(name, "skill", "trigger", "market"):
			if !c.HasSkill {
				c.HasSkill = true
				c.SkillName = name
			}
		case strings.HasPrefix(name, "mcp_") || strings.Contains(name, "__"):
			if !c.HasMCP {
				c.HasMCP = true
				c.MCPName = name
			}
		case containsAny(name, "web", "fetch", "browser", "http", "url"):
			if !c.HasWeb {
				c.HasWeb = true
				c.WebName = name
			}
		}
	}
	return c
}

func containsAny(s string, subs ...string) bool {
	s = strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (tc ToolCatalog) ToolCallingSection() string {
	var b strings.Builder
	b.WriteString("<tool_calling>\n")
	b.WriteString("- Use specialized tools instead of bash commands when possible — this provides better user experience and safer sandbox boundaries.\n")

	var fileTools []string
	if tc.HasRead {
		fileTools = append(fileTools, fmt.Sprintf("`%s` for reading files", tc.ReadName))
	}
	if tc.HasEdit {
		fileTools = append(fileTools, fmt.Sprintf("`%s` for editing and creating files", tc.EditName))
	}
	if len(fileTools) > 0 {
		b.WriteString(fmt.Sprintf("- For file operations, prefer dedicated tools: %s. Reserve shell commands exclusively for actual system operations.\n",
			strings.Join(fileTools, ", ")))
	}

	if tc.HasSearch {
		b.WriteString(fmt.Sprintf("- Use `%s` for symbol/file discovery before blind globbing — it is faster and more precise.\n", tc.SearchName))
	}

	b.WriteString("- NEVER use bash echo or other command-line tools to communicate thoughts or explanations to the user. Output all communication directly in your response text.\n")
	b.WriteString("</tool_calling>\n")
	return b.String()
}

func (tc ToolCatalog) BackgroundTasksSection() string {
	if !tc.HasExec {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<background_tasks>\n")
	b.WriteString(fmt.Sprintf("- Run long-lived commands (builds, test suites, servers) as background tasks via `%s`, then continue independent work.\n", tc.ExecName))
	b.WriteString("- Do NOT poll background tasks in a tight loop — continue other work and check periodically or when idle.\n")
	b.WriteString("</background_tasks>\n")
	return b.String()
}

func (tc ToolCatalog) TaskManagementSection() string {
	if !tc.HasPlan {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<task_management>\n")
	b.WriteString(fmt.Sprintf("- Use `%s` to break objectives into concrete, ordered steps with present-tense active descriptions.\n", tc.PlanName))
	b.WriteString("- Keep at least one step `in_progress` at all times. Mark each step done immediately upon completion — do not batch completions.\n")
	b.WriteString("- When a blocker genuinely prevents progress, record it explicitly in the plan rather than silently dropping the requirement.\n")
	b.WriteString("</task_management>\n")
	return b.String()
}

func (tc ToolCatalog) SkillSection() string {
	if !tc.HasSkill {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<skill_system>\n")
	b.WriteString(fmt.Sprintf("- When the user asks to use a skill, invoke `%s` with the skill name. Skills are domain-specific capability bundles with their own tool sets and instructions.\n", tc.SkillName))
	b.WriteString("- Skills can restrict which tools are available during their execution — always respect the tool allowlist.\n")
	b.WriteString("</skill_system>\n")
	return b.String()
}
