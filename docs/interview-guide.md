# Code-Agent 秋招面试指南

## 1 分钟项目介绍

> 我做了一个类 Claude Code 的 **Coding Agent 运行时**（Go 服务端 + CLI）。  
> 主链路是 ReAct：意图/Skill → 规划 → 工具调用 → 权限门 → 观察 → 反思/收尾。  
> 核心六工具 + MCP 热装 + Skill/Slash/Hook；记忆分 user/project；上下文 L0–L3 压缩（含 LLM 摘要）。  
> 支持 SubAgent 并行与 worktree 隔离。工程上 DDD 分层与依赖倒置，MySQL/Redis/对象存储，SSE 可观测与审计。

## 架构分层（必画）

```
CLI ──SSE/HTTP──► trigger ──► application ──► domain
                                      ▲
                               infrastructure
                         (LLM/MCP/MySQL/Redis/Blob)
```

- **Domain 无 API 层**：DTO 在 `internal/api/dto` 与 trigger，领域只暴露 Port。  
- **DIP**：MCP Manager 在 infra，domain 只依赖 `IMCPManagerPort`。

## 三个深挖故事

### A. Agent Loop + 安全

- ReAct 循环、tool 角色协议、死循环检测  
- 五层权限：DenyList / PathSandbox / ToolClass / SessionPolicy / Circuit + 批准恢复  
- 工具失败 Reflect；Plan Reviewer 收尾

### B. 扩展生态

- MCP stdio 热装 → ToolBridge 同步 Registry  
- Skill 注入 system；Slash 短路；Hook 生命周期  
- 业务能力（搜索等）走社区 MCP，不堆在核心

### C. 上下文与记忆工程

- Hybrid + L3 LLM summary 落 `session_summary`  
- 大 tool 结果 offload 到本地对象存储  
- memory_save/search + 自动纠正提炼 + prompt 注入

## 诚实边界

| 能力 | 状态 |
|------|------|
| Host Executor（本机侧车） | **已实现** `host-agent` WebSocket；`prefer_host` 时优先本机 |
| S3/MinIO | **minio-go** 优先，失败回落本地目录 |
| OTLP | **OTLP HTTP** 导出（Jaeger all-in-one）；另有 Prometheus `/metrics` |
| Teams | 角色工具白名单 YAML，非多 Agent 组织 OS |
| 编排 | **默认自研 Loop**；可选 **CloudWeGo Eino ReAct**（`orchestrator: eino`），工具仍过 Guard；callbacks→SSE；`/team` 并行子代理 |

### 编排怎么讲

> 主链路自研 ReAct 把权限/MCP 做深；编排层可切换 Eino，tool-calling 与图执行交给框架，GuardedTool 仍挂五层权限，避免框架绕过安全。

## 代码阅读路径（20 分钟）

1. `internal/bootstrap/app.go`  
2. `internal/domain/agent/engine/loop.go`  
3. `internal/domain/security/permission.go`  
4. `internal/domain/subagent/runner.go`  
5. `internal/infrastructure/mcp/manager.go`  
6. `internal/domain/contextx/compressor.go` + `summarizer.go`

## 本地 Demo 脚本

```powershell
go run ./cmd/server -config configs/config.yaml
# 另一终端
powershell -File scripts/eval_smoke.ps1
go run ./cmd/cli --key dev-key
# 试：list files / 并行子代理 / /compact / 记住偏好
```

## 简历 bullet 示例

- 设计并实现 Go Coding Agent 运行时：ReAct、六工具、MCP 热装、Skill/Slash/Hook  
- 五层权限与 HITL 恢复；L0–L3 上下文压缩与跨会话记忆  
- SubAgent 并行 + git worktree 隔离；SSE 过程可观测与审计/metrics  
