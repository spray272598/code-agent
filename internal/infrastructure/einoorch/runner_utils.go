package einoorch

// runner_utils.go — small pure helpers: JSON marshal, persona template, timestamps.

import (
	"encoding/json"
	"fmt"
	"time"
)

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func defaultPersona() string {
	return NewPromptContext().Header() + "\n\n" +
		WorkPolicySection() + "\n\n" +
		`<tool_calling>
- Use specialized tools instead of bash when possible. Prefer dedicated file tools for read/edit over shell commands.
- Reserve shell commands exclusively for actual system operations.
- NEVER use bash echo to communicate with the user. Output all communication in your response text.
</tool_calling>` + "\n\n" +
		`<background_tasks>
- Run long-lived commands (builds, test suites, servers) as background tasks, then continue independent work.
- Do NOT poll background tasks in a tight loop — continue other work and check periodically or when idle.
</background_tasks>` + "\n\n" +
		`<delegation_guidance>
When the user asks you to delegate work, launch the subagents near the start of the work; saying you will delegate but never launching does not satisfy the request. Give each subagent a complete, self-contained brief.
</delegation_guidance>` + "\n\n" +
		CommunicationSection() + "\n\n" +
		FormattingSection()
}

func nowMs() int64 { return time.Now().UnixMilli() }

func idMsg() string {
	return fmt.Sprintf("msg-%d", time.Now().UnixNano())
}
