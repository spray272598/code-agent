# Eino 编排集成说明

## 定位：安全与业务执行深入，编排交给框架

| 自研（深入做） | Eino（少造轮子） |
|----------------|------------------|
| **GuardedTool 横切**：validate / Guard / Hook abort / cache / audit / metrics | ReAct Graph、MaxStep、tool-calling 协议 |
| **上下文**：HistoryLoader 式懒加载 + L0–L3 Compress + Token 预算裁剪 | ChatModel / Stream |
| **动态 System Prompt**：persona + tools + Skill + Memory + budget | MessageModifier / MessageRewriter |
| MCP / Skill / 六工具业务实现 | — |
| HITL：Pending + approve + 继续 / Interrupt | StatefulInterrupt 信号 |

默认 **`orchestrator: native`**；`eino` 为可切换编排后端，**不削弱 Guard**。

## 架构

```
ChatApp
  └─ engine.Runner
        ├─ *engine.Loop                 # native
        └─ *einoorch.Runner             # Eino
              ├─ PromptBuilder          # 动态 system
              ├─ loadAndCompress        # 历史 + Compressor + summary
              ├─ trimSchemaMessages     # Token 预算守卫
              ├─ MessageRewriter        # 图内状态再裁剪
              ├─ GuardedTool 横切链
              └─ MultiAgent (/team)
```

## GuardedTool.InvokableRun 横切顺序

```
1. Interrupt resume state?
2. JSON parse + ValidateArgs
3. Guard.Check → DENY | CONFIRM(+StatefulInterrupt) | ALLOW
4. PreToolUse hook (EmitCheck abort)
5. ResultCache hit (read-only)
6. Execute domain tool
7. Cache put / PostToolUse / Audit / ObserveTool latency
```

相关代码：`tool_adapter.go`、`context.go`（`CrossCut` / `RunContext`）。

## 上下文压缩链（Eino Runner）

```
ListAsMaps(24) → Needs? → ListAsMaps(120)
  → CompressLevels (L0–L3, optional LLM summary)
  → mapsToSchema（保留 tool / toolCallId）
  → trimSchemaMessages(budget*3/4)
  → 预算硬拒绝 (est > budget)
  → Generate/Stream
  → MessageRewriter 图内再 trim
```

## mapsToSchema（修复丢工具历史）

- `role=tool` → `schema.ToolMessage(content, toolCallId, WithToolName)`
- 无 `toolCallId` 时合成 `hist_<hash>`
- `assistant` 可选 `toolCalls` / `TOOL_CALLS_JSON:` 前缀

## Token 预算 + 动态 System Prompt

- `Config.TokenBudget` / `engine.TokenManager`
- `PromptBuilder`：tools 指纹缓存 + 每轮 Memory 注入 + Skill 段
- `agent.eino_stream` 控制 Stream

## 配置

```yaml
agent:
  orchestrator: eino          # or native
  eino_stream: false
  token_budget: 32000
  max_steps: 20
llm:
  use_mock: false
  api_key: "..."
  api_base: ""
  model: "gpt-4o-mini"
```

环境变量：`AGENT_ORCHESTRATOR=eino`

## 代码入口

| 文件 | 职责 |
|------|------|
| `runner.go` | 编排主流程、压缩、预算、resume |
| `tool_adapter.go` | GuardedTool 横切 |
| `messages.go` | mapsToSchema / trim |
| `prompt.go` | 动态 system |
| `callbacks.go` | Eino → SSE |
| `multiagent.go` | /team 并行子代理 |

## 测试

```bash
go test ./internal/infrastructure/einoorch/ -count=1 -v
```

覆盖：权限 deny/confirm、validation、hook abort、cache+audit、mapsToSchema 工具保留、trim、PromptBuilder。

## 秋招话术

> 编排可插拔：native 自研 Loop 与 Eino ReAct 共用 domain 工具。Eino 只负责图与 tool-calling；**安全与业务执行**集中在 GuardedTool 横切与压缩/预算链，框架无法绕过权限与审计。

## 后续

1. 跨进程 Checkpoint 持久化 Interrupt  
2. DeepAgent 替换部分 Teams  
3. CallbackOutput.TokenUsage 进 Prometheus  
