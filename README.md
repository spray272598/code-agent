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

## 快速状态

> 当前：仓库初始化 + 设计文档。实现按 `docs/design.md` Phase 推进。

```bash
# 后续
# go run ./cmd/server -config configs/config.yaml
# go run ./cmd/cli --base http://127.0.0.1:8080
```

## 文档

| 文档 | 内容 |
|------|------|
| [docs/design.md](docs/design.md) | 总体设计、DDD、阶段计划 |
| docs/api.md | API（实现后补充） |
| docs/architecture.md | 架构图（实现后补充） |

## License

MIT
