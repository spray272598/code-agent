# Agent Loop (ReAct)

## Protocol

Each model turn should emit:

```
Thought: <reasoning>
Action: {"name":"tool","args":{...}}   # or array of tools
Final Answer: <user text when done>
```

After tools run, the loop injects:

```
Observation (tool_name):
<result>
```

and prompts:

```
Continue the ReAct loop.
Emit Thought: … then Action or Final Answer.
```

## One step (simplified)

```
1. TokenManager.Pressure? → trim messages / hard stop
2. LLM.Generate(system, messages)
3. ParseReAct(content, nativeToolCalls)
4. If Final Answer → plan review → stream answer → done
5. Else ToolExecutor batch:
     - skill allowlist
     - schema ValidateArgs
     - Guard.Check (incl. MCP)
     - PreToolUse (abort possible)
     - parallel if all read-only else serial
     - Observation + optional Reflect on failure
6. Append FormatReActContinue → next step
```

## Events (SSE)

| Type | Meaning |
|------|---------|
| `thought` | Model reasoning |
| `action` / `tool_call` | Tool invocation |
| `observation` / `tool_result` | Tool output |
| `permission` | Confirm / deny |
| `compress` | Context reduction |
| `answer` / `done` | Final |
| `error` | Failures (llm / budget / persist) |

Heartbeat: comment line `: ping <unix>` every 15s.

## Permission resume

1. Tool hits `confirm` → pending id returned
2. `POST /api/v1/permission/approve` with `"continue": true` (inline resume)
3. Or client sends message `继续` / `continue`

## Token budget

- Pre-run: HistoryLoader + Compressor (L0–L3)
- Mid-loop: TokenManager trim + exhausted stop
- System prompt cached by tools fingerprint + skill id
