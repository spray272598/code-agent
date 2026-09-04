# Eino 编排集成说明

## 定位：安全与业务执行深入，编排交给框架

| 自研（深入做） | Eino（少造轮子） |
|----------------|------------------|
| **GuardedTool 横切**：validate / Guard / Hook abort / cache / audit / metrics | ReAct Graph、MaxStep、tool-calling 协议 |
| **上下文**：HistoryLoader 式懒加载 + L0–L3 Compress + Token 预算裁剪 | ChatModel / Stream |
| **动态 System Prompt**：persona + tools + Skill + Memory + budget | MessageModifier / MessageRewriter |
| MCP / Skill / 六工具业务实现 | — |
| HITL：Pending + approve + 继续 / Interrupt | StatefulInterrupt 信号 |

默认 **`orchestrator: native`**（自研主路径，mock / 真实 LLM 均可）；可选 **`orchestrator: eino`**（需真实 LLM Key，否则诚实降级到 Native Loop）。**不削弱 Guard**；MCP 注册为 `server__tool`，与 core 工具同一 `GuardedTool` 横切。

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

## HITL Resume（双路径）

### A. 图内 resume（默认开启 `eino_graph_resume`）

```
CONFIRM → GuardedTool.StatefulInterrupt(ConfirmInfo)
  → CheckPointStore 持久化图状态（./data/eino-checkpoints）
  → 记录 interrupt id（进程内 map）
  → approve → 「继续」
  → compose.ResumeWithData + WithCheckPointID
  → 工具节点 GetInterruptState → Guard(sessionAllow) → execCross
  → 图继续跑完
失败则 fallback → B
```

### B. 应用层 resume（始终可用 / fallback）

```
TakeReadyResume → GuardedTool(UseInterrupt=false, AutoApprove)
  → hooks / audit / metrics
  → 落库 Action + Observation → 新一轮 ReAct
```

配置：

```yaml
agent:
  eino_graph_resume: true
  eino_checkpoint_dir: "./data/eino-checkpoints"
```

## Stream / Interrupt 检测

| 点 | 行为 |
|----|------|
| `runStream` | 累积 text_delta + tool_call name/args 分片；`io.EOF` 正常结束，**其它错误（含 interrupt）向上返回** |
| `isInterruptErr` | 见下表（公开 API 优先） |
| callbacks | model OnEnd / stream 回调写入 `TokenUsage` → `runStats` |
| MultiAgent | 汇总各子代理 toolCalls + measured/estimated tokens |

### Interrupt detection（Eino v0.9.x 限制）

Eino **未导出**统一的 `isInterruptError` / `Interrupted` 接口；图内 `interruptError` 类型为 unexported。

公开可用：

| API | 覆盖 |
|-----|------|
| `compose.ExtractInterruptInfo(err)` | 图级 interrupt / subGraph interrupt |
| `compose.IsInterruptRerunError(err)` | `InterruptSignal`、legacy interrupt-and-rerun |

本仓库 `isInterruptErr` 顺序：

1. `ExtractInterruptInfo`  
2. `IsInterruptRerunError`  
3. `errors.As` → `GetInterruptContexts() []*compose.InterruptCtx`  
4. **仅**前缀 `interrupt happened` / 子串 `interrupt and rerun`（compose `Error()` 文案）  

**禁止**宽泛 `strings.Contains(..., "interrupt")`，避免 `connection interrupted` 误判。  
若 Eino 未来导出官方 `IsInterruptError`，应优先替换本地实现。

## 测试

```bash
go test ./internal/infrastructure/einoorch/ -count=1 -v
```

覆盖：权限 deny/confirm、validation、hook abort、cache+audit、mapsToSchema 工具保留、trim、PromptBuilder、
`isInterruptErr` 误判防护、`runStats` TokenUsage、HITL resume 执行、tool_call delta 累积、filter tools。

## 后续

1. ~~跨进程 Checkpoint 持久化 Interrupt~~ → `docs/checkpoint-index.md`  
2. ~~DeepAgent vs Teams~~ → `/deep` + `docs/deepagent-vs-teams.md`  
3. ~~CallbackOutput.TokenUsage 进 runStats / MultiAgent 汇总~~  
4. 可选：Eino CheckPointStore + graph ResumeWithData 精确图内恢复  

