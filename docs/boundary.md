# Eino vs 自研边界（产品完善版）

> 目标：编排与对话尽量 Eino；**安全与业务执行、CLI、持久化**自研做深。

## 一句话

**Eino 管「想什么、调哪个工具、怎么流式拼」；我们管「能不能执行、怎么执行、怎么记、怎么审」。**

## 归属表

| 能力 | 归属 | 实现位置 |
|------|------|----------|
| 大模型对话 | **Eino** | `einoorch` + `eino-ext/openai` |
| 流式 token 拼装 | **Eino** | `ChatModel.Stream` / callbacks |
| HTTP SSE / 心跳 / 断连取消 | **自研** | `trigger/http` |
| ReAct / 多代理编排 | **Native Loop（主）/ Eino（可选）** | `engine.Loop` / `einoorch.Runner` |
| Native Loop | **自研主路径** | 默认编排；mock / 真实 LLM 均可运行 |
| 限流 / 熔断 / 审计 | **自研** | Redis rate + Guard L5 + audit |
| 权限沙箱 / HITL | **自研** | `security.Guard` + approve API + CLI |
| GuardedTool 横切 | **自研** | validate→Guard→hook→cache→exec→audit |
| 六工具 file/shell/git | **自研** | `domain/tool/coding` |
| MCP 协议与热装 | **自研适配** | stdio Manager；注册名 `server__tool` |
| MCP 执行 | 过 **GuardedTool** | 与 core 工具同一横切 |
| Skill 安装/触发/depends | **自研** | `domain/skill` → PromptBuilder |
| 记忆 user/project | **自研** | Memory Service + tools |
| 上下文压缩 L0–L3 | **自研** | `contextx`（Eino Runner 复用） |
| 会话持久化 | **自研** | MySQL / SQLite / memory |
| CLI/TUI | **自研** | `cmd/cli` |
| 代码索引 | **自研（可选）** | 未强制；可用 eino Retriever 扩展 |
| Host 本机执行 | **自研** | `cmd/host-agent` + WS |

## 运行时选择

```
orchestrator 默认 = native（自研主路径）
  ├─ orchestrator=native（默认）→ Native Loop（mock / 真实 LLM 均可）
  └─ orchestrator=eino 且有 API Key 且 use_mock=false → Eino Runner（否则诚实降级到 Native Loop）
```

环境变量：`AGENT_ORCHESTRATOR=eino|native`，`LLM_API_KEY`，`LLM_USE_MOCK`。

## MCP 统一入口

```
MCP stdio Manager
  → ToolBridge.Register(MCPTool as ITool)
  → MapRegistry
  → Eino: WrapRegistryCross → GuardedTool
  → Native Loop: Guard.Check before Execute
```

所有 MCP 工具命名：`{server}__{tool}`，描述前缀 `[MCP:server]`，默认走 MCP confirm 策略。

## 不做什么

- 不用 Eino 重写 CLI、沙箱、审计表、限流
- 不把 API Key / 明文路径策略交给框架黑盒
- 不在 domain 直接 import Eino（仅 infrastructure/einoorch）

## 面试表述

> 生产默认 Native Loop 做 ReAct 与 tool-calling；Eino 为可选后端（`orchestrator: eino` + 真实 Key）。无 Key 时仍用 Native Loop，方便本地与 CI。
