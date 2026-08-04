# Code-Agent 学习指导

> 本文档面向开发者，系统性指导 Code-Agent 项目的架构、核心流程、代码组织和面试准备。

---

## 目录

1. [架构总览](#1-架构总览)
2. [核心流程：ReAct 主循环](#2-核心流程react-主循环)
3. [6 大核心工具系统](#3-6-大核心工具系统)
4. [5 层安全闸门](#4-5-层安全闸门)
5. [上下文压缩与 Token 管理](#5-上下文压缩与-token-管理)
6. [双引擎编排：Native Loop + Eino](#6-双引擎编排native-loop--eino)
7. [MCP 协议集成](#7-mcp-协议集成)
8. [Skill 技能包系统](#8-skill-技能包系统)
9. [跨会话记忆](#9-跨会话记忆)
10. [SubAgent + Agent Teams 协同](#10-subagent--agent-teams-协同)
11. [Spec 驱动开发](#11-spec-驱动开发)
12. [Slash Commands + Hook 生命周期](#12-slash-commands--hook-生命周期)
13. [可观测性](#13-可观测性)
14. [部署与运维](#14-部署与运维)
15. [面试话术与应对策略](#15-面试话术与应对策略)
16. [动手实战：从 0 到 1 跑起来](#16-动手实战从-0-到-1-跑起来)

---

## 1. 架构总览

### 六边形架构（Ports & Adapters）

Code-Agent 严格遵循六边形架构，确保业务核心与技术实现解耦。

```
                    ┌─────────────────────────────┐
                    │   adapters (API 层)          │
                    │   cmd/cli  cmd/server        │
                    │   internal/trigger/http/ws   │
                    └──────────────┬───────────────┘
                                   │ 调用 Port 接口
                    ┌──────────────▼───────────────┐
                    │   domain (业务核心 · 纯逻辑)  │
                    │                              │
                    │   agent/engine  agent/plan   │
                    │   security      spec         │
                    │   skill         memory       │
                    │   mcp           subagent     │
                    │   team          hook         │
                    │   slash         contextx    │
                    └──────────────┬───────────────┘
                                   │ 实现 Port 接口
                    ┌──────────────▼───────────────┐
                    │   infrastructure (技术实现)   │
                    │   mysql  redisx  storage     │
                    │   mcp   einoorch  config      │
                    └─────────────────────────────┘
```

**设计原则**：
- Domain 层**不能**引用 Infrastructure 包
- 所有跨层通信通过 Port 接口
- 依赖注入从外部传入，Domain 不创建实例

### 包结构索引

| 路径 | 职责 | 核心类型 |
|------|------|---------|
| `cmd/cli` | CLI 入口 | `main()` → 调用 bootstrap |
| `cmd/server` | HTTP Server 入口 | `main()` → 调用 bootstrap |
| `internal/bootstrap` | 应用装配（DI 根） | `App` struct, `NewApp()` |
| `internal/domain/agent/engine` | ReAct 循环 | `Loop`, `Runner`, `TokenManager` |
| `internal/domain/agent/plan` | 计划系统 | `Plan`, `BuildRulePlan`, `BuildFromSpec` |
| `internal/domain/agent/adapter/port` | Agent 端口接口 | `ILLMPort`, `IToolPort` |
| `internal/domain/security` | 权限闸门 | `Guard`, `Permission`, `path.go` |
| `internal/domain/tool` | 工具注册表 | `MapRegistry`, `Tool`, `Result` |
| `internal/domain/tool/coding` | 核心工具实现 | `fs_tools.go`, `bash.go` |
| `internal/domain/skill` | Skill 系统 | `Service`, `Skill`, `SKILL.md` |
| `internal/domain/mcp` | MCP 协议 | `Service`, `Port` 接口 |
| `internal/domain/memory` | 跨会话记忆 | `Service`, `Memory` |
| `internal/domain/subagent` | 子 Agent | `Runner`, `explore`/`verify`/`general` |
| `internal/domain/team` | Agent Teams | `Team`, 4 种协同模式 |
| `internal/domain/hook` | Hook 系统 | `Bus`, `Event`, `Handler` |
| `internal/domain/slash` | Slash Commands | `Registry`, 12+ 内置命令 |
| `internal/domain/contextx` | 上下文压缩 | `Compressor`, `Summarizer` |
| `internal/domain/spec` | Spec 驱动 | `Service`, `Loader`, `Tracker` |
| `internal/domain/telemetry` | 可观测性接口 | `Sink`, `CounterRegistry` |
| `internal/infrastructure/einoorch` | Eino 引擎 | `Runner`, `ToolAdapter`, `Callbacks` |
| `internal/infrastructure/mcp` | MCP 客户端 | `StdioClient`, `Manager` |
| `internal/infrastructure/mysql` | MySQL 存储 | 各 Repository 实现 |
| `internal/infrastructure/config` | 配置管理 | `Config`, `yaml` 加载 |
| `internal/observability` | 可观测性实现 | `OTelSink`, `PrometheusRegistry` |

---

## 2. 核心流程：ReAct 主循环

### ReAct 协议

Code-Agent 的核心是 ReAct（Reasoning + Acting）循环：

```
┌─────────────┐
│   用户输入   │
└──────┬──────┘
       ▼
┌─────────────┐
│  System      │  ← 注入: 角色 + 工具 + 技能 + 记忆 + Spec + CLAUDE.md
│  Prompt 组装 │
└──────┬──────┘
       ▼
┌─────────────┐     ┌──────────────┐
│  LLM 调用    │────▶│  解析响应    │
└──────┬──────┘     └──────┬───────┘
       ▼                    ▼
┌─────────────┐     ┌──────────────┐
│ Thought     │     │ Action       │
│ 推理分析    │     │ 工具调用     │
└──────┬──────┘     └──────┬───────┘
       │                    ▼
       │             ┌──────────────┐
       │             │ 权限闸门     │ ← 5 层检查
       │             └──────┬───────┘
       │                    ▼
       │             ┌──────────────┐
       │             │ 工具执行     │ ← 并行/串行
       │             └──────┬───────┘
       │                    ▼
       │             ┌──────────────┐
       │             │ Observation  │ ← 结果回注
       │             └──────┬───────┘
       │                    │
       └──────────┬─────────┘
                  ▼
         ┌──────────────┐
         │  Final Answer│ ← 完成
         └──────────────┘
```

### 关键文件

- [loop.go](../internal/domain/agent/engine/loop.go) — 主循环实现
- [react.go](../internal/domain/agent/engine/react.go) — Thought/Action/Observation 解析
- [runner.go](../internal/domain/agent/engine/runner.go) — Runner 接口（双引擎）
- [token_manager.go](../internal/domain/agent/engine/token_manager.go) — Token 预算管理
- [tool_batch.go](../internal/domain/agent/engine/tool_batch.go) — 工具并行/串行调度

### 核心循环伪代码

```go
func (l *Loop) Run(ctx, session, userInput) {
    // 1. 加载历史消息
    history := l.loadHistory(ctx, session)

    // 2. 组装 System Prompt
    sysPrompt := l.buildSystemPrompt(tools, skill, memory, spec)

    // 3. 构建计划（Spec 优先，规则兜底）
    plan := l.buildPlan(userInput, specService)

    // 4. ReAct 循环
    for step := 0; step < maxSteps; step++ {
        // 4a. 压缩检查
        if tokenManager.Pressure() > threshold {
            l.compress(ctx, history)
        }

        // 4b. 调用 LLM
        response := l.llm.Chat(ctx, sysPrompt, history, userInput)

        // 4c. 解析响应
        thought, actions, final := l.parseResponse(response)

        // 4d. 如果有工具调用
        for _, action := range actions {
            // 权限检查
            if !l.perm.Check(action.Tool, action.Args, session) {
                // 请求用户确认
                return awaitPermission(action)
            }
            // 执行工具
            result := l.execute(action)
            // 回注结果
            history = append(history, result)
        }

        // 4e. 如果有最终答案
        if final != "" {
            return final
        }
    }
}
```

---

## 3. 6 大核心工具系统

### 工具列表

| 工具 | 实现文件 | 核心能力 |
|------|---------|---------|
| `read_file` | [fs_tools.go](../internal/domain/tool/coding/fs_tools.go) | offset/limit 读取，超长截断 |
| `write_file` | [fs_tools.go](../internal/domain/tool/coding/fs_tools.go) | 自动创建目录 |
| `edit_file` | [fs_tools.go](../internal/domain/tool/coding/fs_tools.go) | **精确替换 + 正则替换 + 全替换** |
| `bash` | [bash.go](../internal/domain/tool/coding/bash.go) | 进程隔离、超时、stdout/stderr 分离、Windows 支持 |
| `glob` | [fs_tools.go](../internal/domain/tool/coding/fs_tools.go) | doublestar `**` 递归、200 条上限 |
| `grep` | [fs_tools.go](../internal/domain/tool/coding/fs_tools.go) | 正则、上下文行、glob 过滤、200 条/文件上限 |

### 额外工具

| 工具 | 实现文件 | 核心能力 |
|------|---------|---------|
| `memory_save` | [memory_tools.go](../internal/domain/tool/coding/memory_tools.go) | 持久化用户/项目记忆 |
| `memory_search` | [memory_tools.go](../internal/domain/tool/coding/memory_tools.go) | 检索跨会话记忆 |
| `delegate` | [delegate 相关](../internal/domain/subagent/) | 子代理派发 |

### 工具执行调度

```
只读工具 (glob, grep, read_file)
    ↓ 并行执行 (信号量控制)
写工具 (write_file, edit_file)
    ↓ 串行执行
Bash 工具
    ↓ 串行执行 + 用户确认
```

---

## 4. 5 层安全闸门

### 层级架构

```
请求进入
    │
    ▼
┌─────────────────────────┐
│  L1: 命令黑名单          │ ← rm -rf, dd, fork bomb, force push...
└──────────┬──────────────┘
           ▼
┌─────────────────────────┐
│  L2: 路径沙箱            │ ← 阻止 ../ 逃逸 + 编码绕过防护
└──────────┬──────────────┘
           ▼
┌─────────────────────────┐
│  L3: 工具分级            │ ← read=allow, write=confirm, bash=confirm
└──────────┬──────────────┘
           ▼
┌─────────────────────────┐
│  L4: 会话放行            │ ← 用户批准后 session 内放行
└──────────┬──────────────┘
           ▼
┌─────────────────────────┐
│  L5: 拒绝熔断            │ ← 连续 5 次拒绝后自动熔断
└──────────┬──────────────┘
           ▼
      工具执行
```

### 关键文件

- [permission.go](../internal/domain/security/permission.go) — 权限检查核心
- [path.go](../internal/domain/security/path.go) — 路径验证 + 编码绕过防护
- [permission_test.go](../internal/domain/security/permission_test.go) — 安全测试

### 面试亮点

展示 `TestDenyBypassAttempts` 测试覆盖：
- `rm -rf /` 的 8 种绕过方式（空格变体、大小写、URL 编码、分号注入...）
- 路径 `..%2f`、`..\`、`全角空格` 等编码绕过

---

## 5. 上下文压缩与 Token 管理

### 4 级压缩策略

```
L0: 单条消息截断（零成本，无 LLM）
    │
    ▼ Token 预算 70% 触发
L1: 优先级保留（错误/高优先级不压缩）
    │
    ▼ Token 预算 85% 触发
L2: 仅保留最近 N 条（硬截断）
    │
    ▼ Token 预算 95% 触发
L3: LLM 摘要生成（保留目标/决策/文件路径/错误/下一步）
```

### 关键文件

- [compressor.go](../internal/domain/contextx/compressor.go) — 压缩策略核心
- [summarizer.go](../internal/domain/contextx/summarizer.go) — LLM 摘要
- [token_manager.go](../internal/domain/agent/engine/token_manager.go) — 实时预算监测

### 设计亮点

- **实时监测**：每个 LLM 调用后检查 token 使用率
- **紧急修剪**：mid-loop 发现压力时立即压缩
- **智能保留**：压缩时保留错误消息和高优先级操作
- **懒加载**：历史消息按需加载，不一次性全量加载

---

## 6. 双引擎编排：Native Loop + Eino

### 架构

Code-Agent 支持两种编排引擎，通过 `config.yaml` 切换：

```yaml
orchestrator:
  type: native    # native | eino
```

### Native Loop（自研）

- **文件**: [loop.go](../internal/domain/agent/engine/loop.go)
- **特点**: 精确控制每一步，适合简单任务
- **协议**: 严格 ReAct（Thought → Action → Observation）

### Eino Engine（CloudWeGo Eino 框架）

- **文件**: [einoorch/runner.go](../internal/infrastructure/einoorch/runner.go)
- **特点**: 图编排，支持复杂 DAG 和多分支
- **协议**: 基于 Eino Graph 的状态机
- **组件**:
  - [tool_adapter.go](../internal/infrastructure/einoorch/tool_adapter.go) — 工具适配 + 权限检查
  - [callbacks.go](../internal/infrastructure/einoorch/callbacks.go) — 生命周期事件 → SSE
  - [multiagent.go](../internal/infrastructure/einoorch/multiagent.go) — 并行子 Agent
  - [agent_build.go](../internal/infrastructure/einoorch/agent_build.go) — Agent 构建

### 切换逻辑

```go
// runner.go
func NewRunner(llm port.ILLMPort, cfg *config.Config) Runner {
    if cfg.Orchestrator.Type == "eino" {
        return einoorch.NewRunner(llm, ...)
    }
    return NewNativeRunner(...)
}
```

---

## 7. MCP 协议集成

### 架构

```
┌──────────────┐     JSON-RPC 2.0      ┌──────────────┐
│  Code-Agent   │ ◄────────────────────► │  MCP Server  │
│  (MCP Client) │      stdio 传输        │  (外部工具)   │
└──────┬───────┘                        └──────────────┘
       │
       ▼
┌──────────────┐
│  权限闸门     │ ← 每个 MCP 工具调用都经过权限检查
└──────────────┘
```

### 关键文件

- [stdio_client.go](../internal/infrastructure/mcp/stdio_client.go) — stdio 传输客户端
- [manager.go](../internal/infrastructure/mcp/manager.go) — 多服务器生命周期管理
- [protocol.go](../internal/infrastructure/mcp/protocol.go) — JSON-RPC 2.0 协议
- [sync.go](../internal/domain/mcp/service/sync.go) — ToolBridge（MCP 工具 → Agent 工具注册表）

### 核心功能

- **3 个核心方法**: `initialize`, `tools/list`, `tools/call`
- **Watchdog 重连**: 15s 心跳，崩溃自动恢复
- **工具同步**: MCP 工具自动注册到 Agent 工具注册表
- **权限检查**: 每个 MCP 工具调用都经过 5 层权限闸门

---

## 8. Skill 技能包系统

### Skill = Prompt + Tools + Resources

```yaml
# SKILL.md
---
id: code-review
name: Code Review
tools: [read_file, grep, glob, bash]
depends: [git]
---

## Instructions
You are a code review expert. Focus on:
- Bug detection
- Performance issues
- Security vulnerabilities
```

### 关键文件

- [service.go](../internal/domain/skill/service.go) — Skill 管理核心
- [model.go](../internal/domain/skill/model.go) — Skill 数据模型

### 核心能力

- **触发匹配**: 关键词匹配 + 评分机制自动激活 Skill
- **工具白名单**: Skill 激活时只允许使用指定工具
- **依赖组合**: `depends:` 链式组合（深度优先、循环检测）
- **热重载**: 无需重启即可加载新 Skill

---

## 9. 跨会话记忆

### 双作用域设计

```
┌──────────────────────────────────────────┐
│  User Memory                              │
│  "用户喜欢用 TypeScript"                   │
│  "用户偏好函数式编程风格"                  │
├──────────────────────────────────────────┤
│  Project Memory                           │
│  "本项目使用 Go 1.22"                     │
│  "测试框架使用 testify"                    │
│  "API 风格遵循 RESTful"                    │
└──────────────────────────────────────────┘
```

### 关键文件

- [service.go](../internal/domain/memory/service.go) — 记忆服务
- [memory_port.go](../internal/domain/memory/adapter/port/memory_port.go) — 记忆端口接口

### 核心功能

- **自动提取**: 检测 "记住"、"以后" 等纠错短语自动存入记忆
- **重要性评分**: 1-100 分，高重要性优先保留
- **Prompt 注入**: `FormatForPrompt()` 将记忆注入 system prompt
- **持久化**: 通过 Repository 接口支持 MySQL/SQLite

---

## 10. SubAgent + Agent Teams 协同

### SubAgent 角色

| 角色 | 权限 | 用途 |
|------|------|------|
| `explore` | 只读工具 | 代码探索、分析 |
| `verify` | 读 + bash | 运行测试、验证结果 |
| `general` | 全工具 | 通用任务执行 |

### Agent Teams 协同模式

```
parallel:   多个 Agent 同时执行不同子任务
review:     一个 Agent 写代码，另一个审查
debate:     两个 Agent 辩论，系统裁决
merge:      多个 Agent 的结果合并
```

### 关键文件

- [subagent/runner.go](../internal/domain/subagent/runner.go) — 子代理执行
- [team/team.go](../internal/domain/team/team.go) — Agent Teams 配置
- [worktree/manager.go](../internal/domain/worktree/manager.go) — git worktree 隔离

### Worktree 隔离

```
main repo
    ├── worktree/agent-1/  ← Agent 1 的独立工作区
    ├── worktree/agent-2/  ← Agent 2 的独立工作区
    └── worktree/review/   ← 审查 Agent 的工作区
```

---

## 11. Spec 驱动开发

### 三件套

```
项目根目录/
├── spec.md        ← 目标 + 约束 + 验收标准
├── tasks.md       ← 子任务列表（自动追踪进度）
├── checklist.md   ← 验收检查清单
└── CLAUDE.md      ← 项目级指令（代码规范、架构约束）
```

### Spec 文件格式

```markdown
---
id: feature-name
title: 功能标题
goal: 一句话描述目标
constraints:
  - 必须使用现有组件库
  - API 响应 p99 < 200ms
acceptance:
  - 页面加载 < 2s
  - 所有组件无错误
---

# 详细描述

## 技术方案
...
```

### 自动追踪

```go
// 完成任务时自动更新 tasks.md
specSvc.MarkTaskDone("task-1")

// 验收通过时自动更新 checklist.md
specSvc.MarkChecklistDone("Dashboard loads within 2 seconds")
```

### 关键文件

- [spec/service.go](../internal/domain/spec/service.go) — Spec 服务
- [spec/loader.go](../internal/domain/spec/loader.go) — 文件解析
- [spec/tracker.go](../internal/domain/spec/tracker.go) — 进度追踪
- [spec/model.go](../internal/domain/spec/model.go) — 数据模型

---

## 12. Slash Commands + Hook 生命周期

### Slash Commands

| 命令 | 功能 |
|------|------|
| `/help` | 帮助信息 |
| `/clear` | 清除会话历史 |
| `/compact` | 手动触发上下文压缩 |
| `/tools` | 列出可用工具 |
| `/skills` | 列出可用 Skill |
| `/mcp` | 管理 MCP 服务器 |
| `/cost` | 显示 token 消耗统计 |
| `/memory` | 管理跨会话记忆 |
| `/teams` | 管理 Agent Teams |
| `/index` | 代码索引管理 |
| `/skill <id>` | 激活指定 Skill |
| `/team <name>` | 激活指定 Team |

### Hook 生命周期事件

```
SessionStart → PreToolUse → PostToolUse → PreCompact → PermissionDecision → SessionEnd
```

### 关键文件

- [slash.go](../internal/domain/slash/slash.go) — Slash Commands 注册
- [hook.go](../internal/domain/hook/hook.go) — Hook 总线

---

## 13. 可观测性

### 三大支柱

```
┌─────────────────────────────────────────┐
│  Traces (OTLP/Jaeger)                   │
│  Agent.Run → LLM.Call → Tool.Execute   │
├─────────────────────────────────────────┤
│  Metrics (Prometheus)                   │
│  token_usage_total, tool_calls_total,  │
│  compress_count, permission_denied_total│
├─────────────────────────────────────────┤
│  Logs (结构化日志)                       │
│  [agent] [spec] [mcp] [security]        │
└─────────────────────────────────────────┘
```

### 关键文件

- [obs.go](../internal/observability/obs.go) — 可观测性入口
- [metrics.go](../internal/observability/metrics.go) — Prometheus 指标
- [prometheus.go](../internal/observability/prometheus.go) — Prometheus Registry 实现
- [telemetry.go](../internal/domain/telemetry/telemetry.go) — 接口定义

---

## 14. 部署与运维

### Docker 部署

```bash
# 一键启动
cp .env.example .env
docker compose --profile app up -d

# 开发模式（带调试工具）
docker compose --profile dev up -d
```

### 服务地址

| 服务 | 地址 | 说明 |
|------|------|------|
| Code-Agent Server | http://localhost:8080 | 主服务 |
| MySQL | localhost:3306 | 数据库 |
| Redis | localhost:6379 | 缓存 |
| MinIO | http://localhost:9000 | 对象存储 |
| Jaeger | http://localhost:16686 | 链路追踪 |

### Dockerfile 双目标

- `target: server` — distroless 生产镜像（最小攻击面）
- `target: server-dev` — debian-slim 调试镜像（带 bash/curl）

### 详细文档

- [docker-up.md](../scripts/docker-up.md) — Docker 部署完整指南
- [.env.example](../.env.example) — 环境变量模板

---

## 15. 面试话术与应对策略

### Q1: 这和 Claude Code / Cursor 有什么区别？

> 承认灵感，强调差异：
>
> 1. **纯 Go 实现**——适合后端服务部署，不依赖 Node.js/Electron
> 2. **六边形架构**——LLM、MCP、存储都可以替换，domain 层零框架依赖
> 3. **双引擎编排**——自研 Loop + Eino 框架可配置切换，适合不同复杂度的任务
> 4. **5 层安全闸门**——从命令黑名单到熔断的纵深防御，这在开源 Agent 中很少见
> 5. **多 Agent 协同**——Worktree 隔离 + 4 种协同模式（parallel/review/debate/merge）
> 6. **Spec 驱动开发**——三件套 + CLAUDE.md，让 AI 按施工图纸干活

### Q2: 你的上下文压缩是怎么做的？

> 强调工程深度：
>
> 我实现了 L0-L3 四级压缩策略：
> - L0：单条消息截断，零成本
> - L1：优先级保留，错误消息/高优先级不压缩
> - L2：仅保留最近 N 条
> - L3：LLM 生成摘要，保留目标/决策/文件路径/错误/下一步
>
> 同时有 TokenManager 实时预算监测，到达阈值时紧急 mid-loop 修剪。
> 所有压缩都不丢关键信息——这通过 `Compressor` 的 `Priority` 分级实现。

### Q3: MCP 是怎么接入的？

> 强调协议完整性：
>
> 完整实现了 JSON-RPC 2.0 协议的 stdio 客户端，支持 initialize/tools/list/tools/call 三个核心方法。
> Manager 负责多服务器生命周期管理，Watchdog 自动重连崩溃的服务器，
> ToolBridge 自动将 MCP 工具同步到 Agent 工具注册表，每个调用都经过 5 层权限闸门检查。
> 这样外部工具不需要自己造轮子就能接入。

### Q4: 安全是怎么保障的？

> 展示测试证据：
>
> 5 层权限闸门，每层都有具体机制：
> - L1 命令黑名单：覆盖 rm -rf、dd、fork bomb、force push 等
> - L2 路径沙箱：阻止 ../ 逃逸，还防 URL 编码、Unicode、全角空格绕过
> - L3 工具分级：read=allow, write=confirm, bash=confirm
> - L4 会话放行：用户批准后在 session 内永久/一次性放行
> - L5 拒绝熔断：连续 5 次拒绝后自动熔断
>
> 重点：`TestDenyBypassAttempts` 测试覆盖了 8 种 rm -rf 绕过方式。

### Q5: Eino 框架的集成价值是什么？

> 讲架构选择：
>
> 自研 Loop 精确控制每一步，适合简单任务；Eino Graph 适合复杂 DAG，
> 支持并行子 Agent 等高级编排。通过配置切换，不是非此即彼。
> 关键是安全层（5 层权限闸门）是自写的，不依赖 Eino——因为 Eino 框架本身不负责安全。

### Q6: 项目的创新点在哪里？

> 突出差异化：
>
> 1. **Spec 驱动开发**——用 spec.md/tasks.md/checklist.md 三件套给 AI 一套施工图纸
> 2. **双引擎架构**——自研 + Eino，灵活选择
> 3. **纵深防御安全**——5 层闸门 + 编码绕过防护 + 熔断
> 4. **跨会话记忆**——user/project 双作用域，自动纠错提取
> 5. **多 Agent 协同**——Worktree 隔离 + 4 种模式
> 6. **可观测性**——Traces + Metrics + Logs 三大支柱

---

## 16. 动手实战：从 0 到 1 跑起来

### 环境准备

```bash
# Go 1.22+
go version

# 克隆项目
git clone <repo-url>
cd code-agent

# 安装依赖
go mod download
```

### 运行测试

```bash
# 所有测试
go test ./internal/...

# 特定包
go test -v ./internal/domain/agent/engine/
go test -v ./internal/domain/security/
go test -v ./internal/domain/spec/
```

### 本地开发（Mock LLM）

```bash
# 使用 Mock 模型启动
go run ./cmd/server --config configs/config.yaml

# 打开浏览器
open http://localhost:8080
```

### 真实模型部署

```bash
# 设置环境变量
export LLM_USE_MOCK=false
export LLM_API_KEY=your-api-key
export LLM_MODEL=deepseek-ai/DeepSeek-V3

# 启动
go run ./cmd/server
```

### Docker 部署

```bash
# 复制环境变量
cp .env.example .env

# 一键启动
docker compose --profile app up -d

# 查看日志
docker compose logs -f server

# 查看服务状态
docker compose ps
```

### 开发 Spec 驱动功能

```bash
# 1. 在项目根目录创建 spec.md
cat > spec.md << 'EOF'
---
id: my-feature
title: My Feature
goal: Implement X feature
constraints:
  - Must use existing framework
acceptance:
  - Works on Linux/macOS/Windows
---

# My Feature
...
EOF

# 2. 创建 tasks.md
cat > tasks.md << 'EOF'
---
status: auto
---

# Tasks
- [ ] task-1: Design API
- [ ] task-2: Implement backend
- [ ] task-3: Write tests
EOF

# 3. 创建 checklist.md
cat > checklist.md << 'EOF'
# Acceptance Checklist
- [ ] API returns correct data
- [ ] Response time < 200ms
EOF

# 4. 启动 Agent，它会自动加载 spec
go run ./cmd/server
```

### 调试技巧

```bash
# 查看 Token 消耗
curl -s http://localhost:8080/api/cost

# 查看 MCP 状态
curl -s http://localhost:8080/api/mcp

# 查看 Skill 列表
curl -s http://localhost:8080/api/skills

# 查看 Spec 进度
curl -s http://localhost:8080/api/spec
```

---

## 附录：学习路径建议

### 初级（1-2 天）

1. 先读 [README.md](../README.md) 了解项目
2. 跑通 `go test ./internal/...` 确保环境没问题
3. 用 Mock LLM 启动，在浏览器里体验
4. 读 [loop.go](../internal/domain/agent/engine/loop.go) 理解 ReAct 循环

### 中级（3-5 天）

5. 读 [security/permission.go](../internal/domain/security/permission.go) 理解 5 层闸门
6. 读 [tool/coding/fs_tools.go](../internal/domain/tool/coding/fs_tools.go) 理解工具实现
7. 读 [contextx/compressor.go](../internal/domain/contextx/compressor.go) 理解压缩
8. 读 [spec/service.go](../internal/domain/spec/service.go) 理解 Spec 驱动
9. 读 [einoorch/runner.go](../internal/infrastructure/einoorch/runner.go) 理解 Eino 集成

### 高级（1-2 周）

10. 读 [mcp/manager.go](../internal/infrastructure/mcp/manager.go) 理解 MCP 生命周期
11. 读 [memory/service.go](../internal/domain/memory/service.go) 理解记忆系统
12. 读 [subagent/runner.go](../internal/domain/subagent/runner.go) 理解子代理
13. 读 [team/team.go](../internal/domain/team/team.go) 理解多 Agent 协同
14. 读 [obs.go](../internal/observability/obs.go) 理解可观测性
15. 完整读一遍 [CLAUDE.md](../CLAUDE.md) 理解项目规范

### 面试准备

16. 准备 Q1-Q6 的话术
17. 能画出 ReAct 循环流程图
18. 能画出 5 层安全闸门架构图
19. 能说出 Native Loop 和 Eino 的区别
20. 能演示 Spec 驱动开发流程
