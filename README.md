# Code-Agent

类 Claude Code 的 **Coding Agent 运行时**：Go 服务端 + CLI 客户端。

- 远程仓库：`git@github.com:spray272598/code-agent.git`
- 设计文档：**[docs/design.md](docs/design.md)**（必读）
- 参考：`walicode-server` / `walissh-client` / `ai-desktop-assistant`

## 形态

| 组件 | 说明 |
|------|------|
| `cmd/server` | Agent 服务端（ReAct、MCP、权限、记忆、SSE） |
| `cmd/cli` | 终端客户端（流式多轮、权限确认） |
| MySQL | 会话 / 消息 / 记忆 / MCP / 审计 |
| Redis | 限流、会话热点、Token 计量、权限会话缓存 |
| 对象存储 | 大工具结果、导出、Skill 资源（S3/MinIO） |

## 快速开始（Phase 1 MVP）

```bash
# 依赖（可选）：MySQL / Redis / MinIO
# docker compose up -d

# 本地 memory + mock LLM（零依赖）
# configs/config.yaml 默认 database.type=memory, llm.use_mock=true

go run ./cmd/server -config configs/config.yaml

# 另一终端
go run ./cmd/cli --base http://127.0.0.1:8080 --key dev-key

# 或 curl
curl -H "X-API-Key: dev-key" http://127.0.0.1:8080/health
```

环境变量：`LLM_API_KEY` / `LLM_API_BASE` / `LLM_MODEL` / `LLM_USE_MOCK` / `CODE_AGENT_API_KEY` / `DB_TYPE` / `WORKSPACE_ROOT`

### 已实现（Phase 1）

- CLI + Server（SSE 流式事件）
- ReAct 循环 + 六大工具：read/write/edit/bash/glob/grep
- 5 层权限骨架 + 批准后继续
- MySQL / memory 仓储；Redis 限流与 token 计数（可选）
- 上下文 Hybrid 压缩
- API Key 鉴权（`X-API-Key` / Bearer）

### Phase 2 已加

- MCP stdio 热装 + 自动加载 `mcp-demo`；API `/api/v1/mcp/*`
- Skill（`skills/*/SKILL.md`）+ `/api/v1/skills/*`
- Slash：`/help` `/tools` `/skills` `/mcp` `/compact` …
- Hook 生命周期日志（Session/Tool/Permission/Compact）

```bash
go build -o mcp-demo.exe ./cmd/mcp-demo   # 供 MCP demo 热装
go run ./cmd/server -config configs/config.yaml
```

### Phase 3 记忆

- 工具：`memory_save` / `memory_search`（scope=user|project）
- API：`GET/POST /api/v1/memory`、`GET /api/v1/metrics`
- 自动：用户纠正/偏好话术写入；每轮 prompt 注入相关记忆

### 待做

对象存储落大结果、LLM 摘要压缩、SubAgent、Worktree、更强可观测（OTLP）

## 文档

| 文档 | 内容 |
|------|------|
| [docs/design.md](docs/design.md) | 总体设计、DDD、阶段计划 |
| docs/api.md | API（实现后补充） |
| docs/architecture.md | 架构图（实现后补充） |

## License

MIT
