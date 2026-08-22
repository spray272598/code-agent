# Code-Agent 秋招面试指南

## 1 分钟项目介绍

> 我做了一个类 Claude Code 的 **Coding Agent 运行时**（Go 服务端 + CLI）。  
> **编排默认 CloudWeGo Eino ReAct**（tool-calling/图/多代理）；**安全与业务执行自研**：GuardedTool 横切、五层权限、Workspace 工具、HITL。  
> MCP 热装为 `server__tool` 与 core 工具同一 Guard；Skill/记忆/L0–L3 压缩/SSE/审计/限流仍在 domain+trigger。  
> 无 API Key 时自动 **native-offline** 兜底，方便本地与 CI。

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

- ReAct 循环、tool 角色协议、死循环检测（SHA256 签名连续相同 2 次触发 reflect）
- 五层权限：DenyList / PathSandbox / ToolClass / SessionPolicy / Circuit + 批准恢复
- **命令归一化**：URL 编码（双重/三重）、Shell 引号清理、零宽字符剥离、Hex/Octal/Unicode 转义、`${IFS}` 替换、多种变体生成
- **路径归一化**：Null 字节剥离、URL 解码（最多 3 层）、Unicode 斜杠替换（`\u2215`、`\u2044`、`\uFF0F`）、Windows 尾部点/空格清理
- 工具批量执行：只读并行（MaxParallelToolCalls=5），写操作串行
- Token 预算压力：TokenManager.Pressure() 触发 mid-loop trim
- 工具失败 Reflect；Plan Reviewer 收尾

### B. 扩展生态

- MCP stdio 热装 → ToolBridge 同步 Registry
- **MCP 熔断器**：per-server 状态机（normal/retry/open/half_open）+ 指数退避（15s→30s→60s→...→5min）
- **Per-user MCP 隔离**：UserFactory + AssertTenant 运行时租户检查
- Skill 注入 system；Slash 短路；Hook 生命周期（PreToolUse/PostToolUse abort）
- 业务能力（搜索等）走社区 MCP，不堆在核心

### C. 上下文与记忆工程

- L0-L3 四级压缩：L0 截断长内容、L1 保留高优先级消息、L2 仅保留最近 N 条 + summary、L3 LLM 生成中间摘要
- **预测性压缩**：CompactThresholdRatio = 0.8（80% 预算时触发 backgroundSummarize()，不阻塞响应）
- **TokenManager**：Pressure() 预算压力检测、Exhausted() 硬停止、TrimMessages() mid-loop 紧急裁剪（保留 head + tail 6 条）
- 大 tool 结果 offload 到本地对象存储（MinIO）
- 记忆混合搜索：关键词召回 → 向量重排 → 余弦相似度 → 重要性加权
- **去重机制**：dupThreshold = 0.88（余弦相似度阈值）
- **自动提取**：触发词预过滤（"不对"、"记住"、"prefer" 等）→ LLM 提取器 → 规则降级

## 诚实边界

| 能力 | 状态 |
|------|------|
| Host Executor（本机侧车） | **已实现** `host-agent` WebSocket；`prefer_host` 时优先本机 |
| S3/MinIO | **minio-go** 优先，失败回落本地目录 |
| OTLP | **OTLP HTTP** 导出（Jaeger all-in-one）；另有 Prometheus `/metrics` |
| Teams | 角色工具白名单 YAML，非多 Agent 组织 OS |
| 编排 | **默认 CloudWeGo Eino ReAct**；无 API Key 时自动 **native-offline** 兜底；工具仍过 Guard |
| SubAgent | 三类角色（explore/verify/general）；支持 worktree + process 双模式隔离；窗口隔离回写（SummarizeResult） |
| MCP | 热装 + 心跳 + 熔断器（per-server 状态机）+ per-user 租户隔离 |
| 意图路由 | **已实现** normal/deep/team 三分流 + 跨轮指代消解 |
| KMS | **已实现** AES-256-GCM 加密存储 SSH 凭据和 LLM Key |

## 已实现但面试指南未提及的能力

1. **意图路由器**（`internal/domain/intent/classifier.go`）：normal/deep/team 三分流 + 跨轮指代消解
2. **Spec 驱动开发**：从 spec.md/tasks.md/checklist.md 构建 Plan
3. **KMS 加密**：AES-256-GCM 加密存储 SSH 凭据和 LLM Key
4. **代码索引**（`internal/domain/codeindex/`）：文件级 + chunk 级 RAG 索引
5. **Skill 语义匹配**：向量 + LLM 双路径
6. **异步压缩**：`backgroundSummarize()` 不阻塞响应
7. **Graph Resume**：Eino 图内中断恢复（`compose.CheckPointStore`）

### 编排怎么讲

> 主链路使用 Eino ReAct 负责编排与对话，自研负责安全执行与产品层。GuardedTool 横切确保所有工具（包括 MCP 外部工具）都经过五层权限检查，避免框架绕过安全。

## 代码阅读路径（20 分钟）

1. `internal/bootstrap/app.go`  
2. `internal/domain/agent/engine/loop.go`  
3. `internal/domain/security/permission.go`  
4. `internal/domain/security/normalize.go`（命令归一化）
5. `internal/domain/subagent/runner.go`  
6. `internal/infrastructure/mcp/manager.go`
7. `internal/domain/intent/classifier.go`（意图路由器）
8. `internal/domain/contextx/compressor.go` + `summarizer.go`

## 本地 Demo 脚本

```powershell
go run ./cmd/server -config configs/config.yaml
# 另一终端
powershell -File scripts/eval_smoke.ps1
go run ./cmd/cli --key dev-key
# 试：list files / 并行子代理 / /compact / 记住偏好
```

## 简历 bullet 示例

- 基于 CloudWeGo Eino 实现 ReAct 执行循环与 Plan-Execute-Reflect 可中断重规划；设计 explore/verify/general 三类子代理，支持并行执行与 Git Worktree/进程双模式隔离
- 构建五层纵深防御（命令黑名单、路径沙箱、工具分级授权、会话管控、风险熔断），实现命令/路径归一化（处理 URL 编码、Unicode 变体、零宽字符等绕过），支持三档沙箱降级与 HITL 完整生命周期管理
- 实现 L0-L3 四级 Token 压缩与预测性异步摘要，结合 MinIO 大结果卸载与懒加载，通过 TokenManager 实现 mid-loop 紧急裁剪；子代理结果摘要回写，防止主上下文膨胀
- 实现 MCP 协议客户端（热装、心跳、熔断器、异常重连）与工具桥接，支持 per-user 租户隔离；基于 OpenTelemetry + Prometheus 完成链路追踪与指标采集，SSE 实时推送推理过程
