# Eino 编排集成说明

## 结论

- **默认仍是自研 `engine.Loop`**（`agent.orchestrator: native`）
- **可选切换 CloudWeGo Eino ReAct**（`agent.orchestrator: eino`）
- **不是整仓替换**：权限 Guard、MCP、六工具、记忆、审计仍在 domain；Eino 只负责 **think → tool-call → observe** 图编排

## 为何引入

| 自研 Loop 已有 | Eino 补强 |
|----------------|-----------|
| 权限 / MCP / Skill 深度定制 | 成熟 ReAct Graph / MaxStep |
| HITL confirm 产品语义 | 原生 Function Calling 与流式拼装 |
| 业务事件 SSE | ChatModel / Tool 生态组件 |

「重复造轮子」主要指：**模型 tool-calling 协议、图编排、流拼接**——这些交给 Eino；**安全与业务边界**仍自研。

## 架构

```
ChatApp
  └─ engine.Runner  (port)
        ├─ *engine.Loop          // native
        └─ *einoorch.Runner      // Eino ReAct
                 ├─ openai.ChatModel (eino-ext)
                 ├─ react.NewAgent
                 └─ GuardedTool → domain ITool + Guard.Check
```

## 配置

```yaml
agent:
  orchestrator: native   # 或 eino
  max_steps: 20

llm:
  api_key: "sk-..."
  api_base: "https://api.openai.com/v1"  # 兼容网关可改
  model: "gpt-4o-mini"
  use_mock: false   # eino 模式不要 mock
```

环境变量：`AGENT_ORCHESTRATOR=eino`（若配置支持 env 可扩展）

## 代码入口

| 路径 | 职责 |
|------|------|
| `internal/domain/agent/engine/runner.go` | Runner 接口 |
| `internal/infrastructure/einoorch/runner.go` | Eino ReAct 封装 |
| `internal/infrastructure/einoorch/tool_adapter.go` | ITool → Eino Tool + Guard |
| `internal/bootstrap/app.go` | 按配置选择 orchestrator |

## 秋招话术

> 我们早期自研 ReAct 跑通安全与 MCP；编排层后续接入 Eino，把模型 tool-calling 与图执行交给框架，**权限门仍挂在工具适配器上**，避免框架绕过企业安全策略。

## 已加强（持续）

1. **Callbacks → SSE**：ChatModel / Tool OnStart/OnEnd → `thought` `action` `tool_call` `observation` `permission` `text_delta`
2. **HITL**：`StatefulInterrupt` on CONFIRM；`继续` 时 `TakeReadyResume` 先执行再进 ReAct
3. **Multi-agent**：`/team` 或 `/parallel` 触发 explore+verify 并行 Eino 子代理再 merge
4. **Stream**：`agent.eino_stream: true` 走 `agent.Stream` 推 `text_delta`

## 仍可演进

1. 正式 CheckPoint store 持久化 interrupt（跨进程 resume）
2. DeepAgent 图替换部分 SubAgent YAML
3. 更细的 token usage 从 model.CallbackOutput 上报  

