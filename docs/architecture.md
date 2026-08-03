# Code-Agent Architecture (Ports & Adapters)

## Layers

```
cmd/*                    binaries (server, cli, host-agent, mcp-demo)
internal/trigger         HTTP/SSE/WS adapters (inbound)
internal/application     use-cases (ChatApp)
internal/domain          pure domain (engine, tools, security, memory…)
internal/infrastructure  outbound adapters (LLM, MySQL/SQLite, Redis, MCP stdio, MinIO)
internal/api/dto         edge contracts only (not imported by domain)
```

## Dependency rule

- **Domain never imports infrastructure or trigger.**
- Domain defines ports (`adapter/port` interfaces); infrastructure implements them.
- Application orchestrates domain services; Trigger maps DTO ↔ application.

## Ports (examples)

| Port | Package | Implemented by |
|------|---------|----------------|
| `ILLMPort` | `domain/agent/adapter/port` | `infrastructure/llm` |
| `ISessionRepository` / `IMessageRepository` | `domain/session/adapter/repository` | memory / MySQL / SQLite repos |
| `IMemoryRepository` | `domain/memory/adapter/port` | memory / MySQL / SQLite |
| `IMCPManagerPort` | `domain/mcp/adapter/port` | `infrastructure/mcp.Manager` |
| `blob.Store` | `domain/blob` | MinIO / local FS |

## Agent Loop decomposition

| Component | File | Responsibility |
|-----------|------|----------------|
| **Loop** | `engine/loop.go` | ReAct orchestration, events, session lifecycle |
| **ReActParser** | `engine/react.go` | Thought / Action / Final Answer parsing |
| **ToolExecutor** | `engine/tool_batch.go` | Validate, permission, hook abort, parallel read tools |
| **TokenManager** | `engine/token_manager.go` | Budget pressure + mid-loop trim |
| **HistoryLoader** | `engine/history.go` | Lazy history load (recent first, full on compress) |
| **Eino Runner** | `infrastructure/einoorch/` | Optional ReAct graph; **GuardedTool** owns security cross-cuts |
| **mapsToSchema** | `einoorch/messages.go` | History → Eino msgs **including tool rows** |
| **PromptBuilder** | `einoorch/prompt.go` | Dynamic system: tools + skill + memory + budget |

See also: [eino-integration.md](./eino-integration.md).

## Security layers (Guard)

1. L1 command deny (normalized + multi-pattern + shell segments)
2. L2 path sandbox (URL/Unicode-normalized variants)
3. L3 tool class / MCP default confirm
4. L4 session approvals
5. L5 circuit breaker

## Config entry

- `configs/config.yaml` + env overrides (`LLM_*`, `DB_TYPE=sqlite`, …)
- API keys hashed at boot (`auth.KeyStore`); Host WS uses `Valid` only.
