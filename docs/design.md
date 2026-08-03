# Code-Agent 设计文档

> **定位**：类 Claude Code 的 **Coding Agent 运行时**（服务端 + CLI 客户端）  
> **仓库**：`git@github.com:spray272598/code-agent.git`  
> **本地路径**：`D:\project_go\code-agent`  
> **参考**：`walicode-server`（领域与 Agent 深度）、`walissh-client`（流式交互）、`ai-desktop-assistant`（可迁移底座）  
> **状态**：方案已定，按阶段实现  

---

## 1. 目标与非目标

### 1.1 目标

| 目标 | 说明 |
|------|------|
| Claude Code 式体验 | CLI 多轮对话、LLM **流式**输出、工具调用过程可见 |
| 服务端管控 | 风险、会话、限流、审计集中在 Server（企业常见做法） |
| 六大编程工具 | ReadFile / WriteFile / EditFile / Bash / Glob / Grep |
| Agent Loop | ReAct：拆任务 → 调工具 → 观察 → 再决策 |
| 扩展生态 | MCP 热装、Skill 技能包、Slash 命令、Hook 生命周期 |
| 安全纵深 | 5 层权限：拒绝 / 路径沙箱 / 工具分级 / 会话策略 / 人机确认+熔断 |
| 上下文与记忆 | 多级压缩 + Token 预算；**用户级 + 项目级**跨会话记忆 |
| 协作增强 | SubAgent 并行；Git Worktree 隔离（P2）；Agent Teams 薄实现（P2） |
| 工程基础设施 | MySQL 持久化、Redis 限流/缓存/Token、对象存储、MQ 按需 |

### 1.2 非目标（当前阶段不做或弱化）

- 完整 IDE / VS Code 插件（后续可加）
- 真·多租户 SaaS 计费与组织权限中台
- 桌面 GUI 点击自动化（Computer Use）
- 办公 Office 全家桶（能力走 MCP）
- 分布式 Agent 集群编排（SubAgent 单机并发即可）

### 1.3 产品形态

```
┌─────────────────┐     HTTPS / SSE      ┌──────────────────────────────┐
│  code-agent CLI │ ◄──────────────────► │  code-agent Server           │
│  (本机终端)      │   auth + session     │  Agent Loop / Tools / MCP    │
└────────┬────────┘                      │  Permission / Memory / ...   │
         │ 本地 cwd                       └───────┬──────────────────────┘
         │ 展示流式事件                              │
         ▼                                         ▼
   用户仓库文件                              MySQL · Redis · Object Storage
   (实际读写可走「工作区同步」或 Server 侧挂载路径，见 §5.4)
```

**原则**：CLI 负责交互与呈现；**决策与危险操作裁决在服务端**。  
工具执行位置见 §5.4（本地 Host Agent 侧车 vs Server 内执行）——默认首期采用 **「Server 执行 + 可配置工作区根」**，二期可选 **Host Executor** 操作用户本机仓库。

---

## 2. 技术选型

| 层 | 选型 | 用途 |
|----|------|------|
| 语言 | Go 1.22+ | 服务端 + CLI 同仓 |
| API | HTTP + SSE | 对话流式、权限确认 |
| CLI | Cobra + 彩色日志 / 后续 Bubble Tea | 终端体验 |
| DB | MySQL 8 | 会话、消息、记忆、MCP 配置、审计 |
| 缓存 | Redis 7 | 限流、会话热点、Token 预算计数、权限会话批准 |
| 对象存储 | S3 兼容（MinIO 本地 / 云 OSS） | 大工具结果、截图、导出包、Skill 资源、会话附件 |
| MQ | 按需（Redis Stream 优先，可换 RabbitMQ） | 异步记忆提炼、压缩任务、SubAgent 结果汇聚（可选） |
| LLM | OpenAI 兼容 API | 流式 chat + tool calls |
| 配置 | YAML + 环境变量 | 12-factor |

---

## 3. 严格 DDD 与依赖规则

### 3.1 分层

```
cmd/                    进程入口
internal/trigger/       用户接口：HTTP / SSE / CLI 网关适配
internal/application/   用例编排（无业务规则细节）
internal/domain/        核心业务 + **仅依赖 port**
internal/infrastructure/ 技术实现（MySQL/Redis/S3/LLM/MCP/FS）
internal/bootstrap/     Composition Root（唯一装配处）
internal/types/         枚举、错误码、常量
```

### 3.2 依赖方向（强制）

```
trigger → application → domain ← infrastructure
                ↑
           只依赖 domain 定义的 port / repository 接口

禁止：domain import infrastructure
禁止：domain import trigger
允许：infrastructure import domain（实现接口）
允许：bootstrap import 所有层
```

### 3.3 限界上下文（域）

| 域 | 职责 |
|----|------|
| `agent` | Loop、ReAct、Plan、Reflect、事件 |
| `tool` | 六大工具契约与注册表 |
| `security` | 5 层权限、awaiting 恢复 |
| `contextx` | Token 预算、多级压缩 |
| `memory` | 用户级 / 项目级记忆 |
| `mcp` | 安装、热加载、工具同步策略（port） |
| `skill` | SKILL.md 加载匹配 |
| `slash` | 斜杠命令 |
| `hook` | 生命周期钩子 |
| `subagent` | 子任务分发与汇聚 |
| `worktree` | Git worktree（P2） |
| `team` | 角色团队配置（P2） |
| `session` | 会话/消息聚合（可放 agent 内） |

### 3.4 核心 Port 清单

```go
// LLM
type ILLMPort interface {
    Generate(ctx, req) (*ChatResponse, error)
    GenerateStream(ctx, req, onDelta) (*ChatResponse, error)
}

// 工具
type ITool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx, args) (ToolResult, error)
}

// MCP（domain 只依赖此接口）
type IMCPClient interface {
    Name() string
    Initialize(ctx) error
    ListTools(ctx) ([]ToolDef, error)
    CallTool(ctx, name, args) (string, error)
    Close() error
}
type IMCPManagerPort interface {
    AddOrUpdate(ctx, ServerConfig) error
    Remove(name) error
    ListTools(ctx) ([]ToolDef, error)
    CallTool(ctx, name, args) (string, error)
    Health(ctx) []HealthStatus
}

// 仓储
ISessionRepository / IMessageRepository / IMilestoneRepository
IMemoryRepository  // scope: user | project
IMCPServerRepository
IAuditRepository

// 基础设施能力
IObjectStorage   // Put/Get/SignURL
IRateLimiter     // Redis
ITokenMeter      // 会话/用户 token 计数
IFilesystemPort / IShellPort / IGitPort
IHookRunner
ISummarizerPort  // 可委托 LLM
```

### 3.5 单测策略

| 层 | 策略 |
|----|------|
| domain | 100% mock port；权限/压缩/计划/slash 必测 |
| application | 用例级 mock repo |
| infrastructure | 契约测（可选 testcontainers） |
| e2e | scripts + Mock LLM |

---

## 4. 功能架构

### 4.1 一次对话主链路

```
CLI 输入
  → Auth / RateLimit(Redis)
  → Session 加载（Redis 热点 → MySQL）
  → Slash 短路？（/compact /memory /mcp …）
  → Skill 匹配 → 注入 System
  → Memory 检索（user + project）注入
  → Context 组装 + Token 预算；必要时 Compress
  → AgentLoop
        stream: thought / text_delta
        tool_call → Hook.Pre → Permission → Execute → Hook.Post
        observation（截断/落对象存储若过大）
        plan_update / reflect
  → 持久化消息 / 里程碑 / token 计量
  → SSE 事件回 CLI
  → 异步：记忆提炼 / 摘要（MQ 或 goroutine）
```

### 4.2 六大编程工具

| 工具 | 行为 | 权限默认 |
|------|------|----------|
| `read_file` | 读文件（限 project root） | ALLOW |
| `write_file` | 整文件写 | CONFIRM |
| `edit_file` | search_replace 或 unified diff 应用 | CONFIRM |
| `bash` | 受控 shell（超时、deny list） | CONFIRM |
| `glob` | 文件名模式匹配 | ALLOW |
| `grep` | 内容正则/固定串搜索 | ALLOW |

外部能力（搜索、GitHub、12306…）→ **MCP**，不进核心。

### 4.3 MCP

- 传输：stdio / SSE（streamable HTTP 可后续）
- 生命周期：安装 → 启动 → ListTools → 合并 ToolRegistry → 热卸载
- 配置持久化 MySQL；进程启动恢复
- **domain 只通过 IMCPManagerPort**

### 4.4 Skill

```
skills/<id>/
  SKILL.md          # frontmatter + 指南
  scripts/          # 可选
  resources/        # 可选；大文件可上对象存储
```

匹配：触发词 / `/skill <id>` / 显式自然语言。  
注入：system 段落 + 可选工具白名单。

### 4.5 Slash Command

| 命令 | 行为 |
|------|------|
| `/help` | 帮助 |
| `/compact` | 触发上下文压缩 |
| `/clear` | 清空展示层上下文（可保留 summary） |
| `/memory` | 查看/写入记忆 |
| `/mcp` | 列表/提示安装 |
| `/skills` | 列表 |
| `/cost` | token 用量 |
| 用户自定义 | `commands/*.md` 或 YAML |

### 4.6 Hook

节点：`SessionStart` `SessionEnd` `PreToolUse` `PostToolUse` `PreCompact` `PermissionDecision`  

实现：配置声明 + 可执行脚本/HTTP webhook；失败策略（ignore / abort）。

### 4.7 五层权限

| 层 | 名称 | 机制 |
|----|------|------|
| L1 | DenyList | 正则绝对拒绝 |
| L2 | PathSandbox | 仅 project_root + 显式允许；敏感路径拦截 |
| L3 | ToolClass | 读允许 / 写与 shell 确认 |
| L4 | SessionPolicy | once / session / always（Redis 可缓存） |
| L5 | HumanGate + Circuit | CLI/API 确认；连续拒绝熔断 |

中断与恢复：`awaiting` 登记 → Approve → CLI「继续」或 `continue=true` 先执行再 Loop。

### 4.8 上下文压缩与 Token

**多级压缩（对标 walicode ContextCompressor）**

| Level | 动作 |
|-------|------|
| L0 | 单条 tool result 截断；超大 → 对象存储 + 摘要指针 |
| L1 | HybridReducer（优先级 ∪ 滑动窗口） |
| L2 | 折叠连续 tool 对 |
| L3 | LLM 生成 session_summary，替换中段历史 |
| L4 | 用户 `/compact` 强制 |

**Token 管理**

- 估算 + 可选 API usage 回写
- Redis：`token:user:{id}:day`、`token:session:{id}`
- 超配额：拒绝或降级模型

### 4.9 跨会话记忆

| Scope | Key | 内容示例 |
|-------|-----|----------|
| `user` | user_id | 偏好、纠正教训 |
| `project` | project_id（root 路径 hash / git remote） | 构建命令、架构约定 |

- 写入：工具 `memory_save` + 纠正检测 + 异步提炼  
- 读取：`memory_search` + 注入 top-k（importance）  
- 压缩：同 category 去重、低 importance 淘汰  

### 4.10 SubAgent / Worktree / Teams

| 能力 | MVP | 说明 |
|------|-----|------|
| SubAgent | P1 | `delegate(task, tools, max_steps)` 并发执行，结果回主循环 |
| Worktree | P2 | `git worktree add` 绑定子 Agent cwd，互不覆盖 |
| Teams | P2 薄 | YAML 角色 → 工具白名单 → 主 Agent 按角色 delegate |

### 4.11 流式事件协议（CLI ↔ Server）

```text
event: session        data: {"sessionId":"..."}
event: thought        data: {"step":1,"content":"..."}
event: text_delta     data: {"text":"..."}
event: tool_call      data: {"name":"grep","args":{...}}
event: tool_result    data: {"name":"grep","preview":"..."}
event: permission     data: {"id":"perm-..","tool":"bash",...}
event: plan           data: {"summary":"...","subTasks":[...]}
event: plan_update    data: {...}
event: compress       data: {"level":3,"savedTokens":1200}
event: skill          data: {"id":"..."}
event: subagent       data: {"id":"...","status":"running"}
event: error          data: {"class":"llm|tool|mcp|permission|loop","message":"..."}
event: answer         data: {"content":"..."}
event: done           data: {"tokenUsed":...}
```

---

## 5. 服务端与 CLI 职责

### 5.1 Server

- REST：会话、MCP、Skill、记忆、权限批准、健康检查
- SSE：`POST /api/v1/chat/stream`
- Agent 全流程、限流、计量、审计日志
- 后台任务：摘要、记忆提炼（goroutine 或 MQ）

### 5.2 CLI

- 登录/配置 API Base / Token
- REPL：输入、渲染 SSE、权限 y/N/session
- 展示 tool 调用与 diff 摘要
- 本地记录最近 sessionId

### 5.3 鉴权（首期）

- API Key 或 JWT（用户 id）
- 请求头：`Authorization: Bearer <token>`
- 后续可接 OAuth

### 5.4 工具执行位置（重要决策）

**Phase 1 默认：Server-side Workspace**

- 服务端配置 `workspace_root` 或按 project 挂载目录
- 适合：服务器上的代码仓、DevContainer、演示环境
- 优点：权限与审计统一、实现简单

**Phase 2 可选：Host Executor（侧车）**

- CLI 或本机 daemon 注册 WebSocket，Server 下发 tool 指令在用户机器执行
- 更像「个人 Claude Code」操作本机仓库
- 实现成本高，作为明确演进项

**当前立项默认 Phase 1**；CLI 仍提供完整 Claude Code 式交互。

---

## 6. 数据模型（MySQL）

### 6.1 表（初版）

| 表 | 用途 |
|----|------|
| `user_account` | 用户与 API Key 哈希 |
| `chat_session` | 会话；含 user_id、project_id、title、token_used、status |
| `chat_message` | 消息；role/content/tool_*/token/priority |
| `chat_milestone` | 里程碑事件 |
| `core_memory` | 长期记忆；scope、project_id、category、importance |
| `session_summary` | 会话摘要（压缩产物） |
| `mcp_server_config` | MCP 配置 |
| `skill_install` | 已安装 skill 元数据（或纯文件系统 + DB 索引） |
| `audit_log` | 工具调用与权限决策审计 |
| `object_meta` | 对象存储键、大小、session 关联 |

### 6.2 Redis Key 设计

| Key | 用途 |
|-----|------|
| `rl:chat:{userId}` | 滑动窗口限流 |
| `sess:hot:{sessionId}` | 会话热点摘要/最近消息 |
| `perm:allow:{sessionId}` | 会话级批准签名 |
| `token:user:{userId}:{yyyyMMdd}` | 日 Token 配额 |
| `token:sess:{sessionId}` | 会话 Token |
| `lock:compact:{sessionId}` | 压缩互斥 |

### 6.3 对象存储路径约定

```
s3://bucket/code-agent/
  {userId}/sessions/{sessionId}/tools/{toolCallId}.txt
  {userId}/exports/...
  {userId}/skills/{skillId}/...
```

---

## 7. 中间件与部署

### 7.1 本地开发

```yaml
# docker-compose：MySQL + Redis + MinIO
# Server: :8080
# CLI: code-agent --base http://127.0.0.1:8080
```

### 7.2 配置项（节选）

```yaml
server:
  addr: ":8080"
mysql: ...
redis: ...
storage:
  endpoint: http://127.0.0.1:9000
  bucket: code-agent
  access_key: ...
  secret_key: ...
llm:
  api_base: ...
  api_key: ...
  model: ...
agent:
  max_steps: 20
  token_budget: 32000
  workspace_root: ./workspace
rate_limit:
  per_minute: 60
mq:
  enabled: false
  # redis_stream / rabbitmq
```

### 7.3 MQ 引入原则

| 场景 | 是否需要 MQ |
|------|-------------|
| 同步对话 | 否 |
| 记忆异步提炼 | 可选（goroutine 足够则先不上） |
| SubAgent 大量并行汇聚 | 可选 |
| 多实例 Server 水平扩展 | 建议 Redis Stream 或 RabbitMQ |

**首期 `mq.enabled=false`**，接口预留 `IAsyncTaskPort`。

---

## 8. 目录结构（目标）

```
code-agent/
├── cmd/
│   ├── server/          # 服务端
│   ├── cli/             # CLI 客户端
│   └── mcp-demo/        # 示例 MCP
├── configs/
│   ├── config.yaml
│   └── config.example.yaml
├── docs/
│   ├── design.md        # 本文档
│   ├── architecture.md  # 实现后补架构图细节
│   ├── api.md
│   └── interview-guide.md
├── skills/              # 内置技能
├── commands/            # 用户 slash
├── hooks/
├── scripts/
│   ├── sql/01_schema.sql
│   └── eval/
├── internal/
│   ├── domain/          # 见 §3
│   ├── application/
│   ├── infrastructure/
│   ├── trigger/
│   │   ├── http/
│   │   ├── sse/
│   │   └── cli/         # CLI 共用 API client 可放这或 pkg/
│   ├── bootstrap/
│   └── types/
├── pkg/                 # 可对外的轻量库（client SDK）
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── README.md
└── .gitignore
```

---

## 9. 从 ai-desktop-assistant 迁移清单

| 组件 | 动作 |
|------|------|
| ReAct Engine | 迁移并改流式、Hook、Reflect |
| PermissionGuard | 迁移并升级 5 层 + path sandbox |
| HybridReducer | 迁入 contextx，包进 Compressor |
| MCP stdio/SSE | 迁到 infrastructure，domain 只 port |
| Skill loader | 迁移增强 |
| LLM OpenAI gateway | 加 Stream |
| Session/Message MySQL | 扩展 project_id / summary |
| Marketplace | 迁移并修 DIP |
| React 控制台 | **不作为主路径**；可选调试页后期再加 |

---

## 10. 分阶段实施计划

### Phase 0 — 建仓与 DIP 骨架（当前）

- [x] 仓库初始化 + remote
- [x] `docs/design.md`
- [ ] go.mod、配置骨架、docker-compose、SQL 草案
- [ ] domain ports 空接口 + 目录
- [ ] CI：`go test ./...` 绿

### Phase 1 — Server MVP + CLI 流式（投递最小闭环）

- 会话 CRUD、Chat SSE、Mock/真 LLM 流式
- 六大工具 + PathSandbox
- 5 层权限（至少 L1–L4）+ 确认恢复
- MySQL 落库 + Redis 限流
- CLI REPL 渲染事件

**验收**：在配置的 workspace 内「搜索 → 读 → 改 → bash 测试」全程流式可见。

### Phase 2 — 扩展生态

- MCP 安装热加载 + 市场
- Skill + Slash + Hook
- 对象存储：大 tool 结果落盘
- Token 日配额

### Phase 3 — 记忆与压缩做深

- 多级 ContextCompressor（含 LLM summary）
- Memory user/project + 工具
- Reflect + Plan Reviewer
- 评测集：长对话 token、记忆命中

### Phase 4 — SubAgent

- delegate 并行、事件、结果汇聚
- 可选 Redis 锁与并发上限

### Phase 5 — Worktree + Teams 薄实现

- git worktree 绑定
- team YAML 角色委托

### Phase 6 — 抛光

- 文档、面试稿、Demo 脚本、安全审计说明

---

## 11. API 草案（节选）

| Method | Path | 说明 |
|--------|------|------|
| GET | `/health` | 健康 |
| POST | `/api/v1/auth/token` | 换票（或静态 API Key） |
| POST | `/api/v1/session` | 创建会话（带 projectId） |
| GET | `/api/v1/session/{id}` | 会话信息 |
| POST | `/api/v1/chat/stream` | SSE 对话 |
| POST | `/api/v1/permission/approve` | 批准（continue 可选） |
| GET/POST | `/api/v1/mcp/servers` | MCP 列表/安装 |
| GET | `/api/v1/mcp/health` | MCP 健康 |
| GET | `/api/v1/skills` | Skills |
| GET/POST | `/api/v1/memory` | 记忆查询/写入 |
| POST | `/api/v1/session/{id}/compact` | 压缩 |
| GET | `/api/v1/usage` | Token 用量 |

---

## 12. 风险与缓解

| 风险 | 缓解 |
|------|------|
| Server 执行改不到用户本机代码 | Phase1 明确 workspace；文档写清；Phase2 Host Executor |
| 流式 + 权限中断复杂 | awaiting 状态机单测覆盖 |
| 压缩丢关键约束 | summary 强制保留 goal + 最近 K 轮 + 记忆 |
| MCP 进程泄露 | Manager 生命周期 + 超时杀进程 |
| 范围膨胀 | 严格按 Phase 验收，P2 可砍 |

---

## 13. 成功标准（秋招可讲述）

1. 能画清：**CLI → Server → Loop → Tools/MCP → 权限 → 压缩/记忆**  
2. 能 Demo：流式改代码 + 权限确认 + MCP 热装  
3. 能说明：DDD 依赖倒置如何服务单测  
4. 能对比：与 walicode 的对齐点、与 Claude Code 的差距（诚实）  
5. 有数据：压缩前后 token、限流与配额行为  

---

## 14. 开放问题（需产品确认）

| # | 问题 | 默认假设 | 状态 |
|---|------|----------|------|
| 1 | 对象存储厂商 | MinIO 本地 + S3 API | 已按此设计 |
| 2 | 首期工具是否必须操作开发者本机目录 | Phase1 Server workspace | **请确认是否接受** |
| 3 | 鉴权 | API Key 首期 | 可改 |
| 4 | 默认模型提供商 | 环境变量注入，不绑死 | OK |
| 5 | 是否保留 Web 调试台 | 二期可选 | OK |

---

## 15. 修订记录

| 日期 | 变更 |
|------|------|
| 2026-08-03 | 初稿：立项、DDD、中间件、分阶段、迁移清单 |
