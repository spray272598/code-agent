# CLAUDE.md — Code-Agent 项目指令

## 项目简介

Code-Agent 是一个纯 Go 实现的 Coding Agent，灵感来自 Claude Code / Cursor。
它支持 CLI 交互和 HTTP Server 两种部署模式，内置 6 大核心工具、5 层安全闸门、MCP 协议和多 Agent 协同。

## 架构原则

### 六边形架构 (Ports & Adapters)

```
┌─────────────────────────────────────────────────────┐
│  adapters (API 层)                                   │
│  cmd/cli  cmd/server  internal/trigger/http/ws      │
└──────────────────────┬──────────────────────────────┘
                       │ 调用端口
┌──────────────────────▼──────────────────────────────┐
│  domain (业务核心)                                    │
│  agent/engine  agent/plan  security  spec  skill    │
│  memory  subagent  team  hook  slash  contextx      │
└──────────────────────┬──────────────────────────────┘
                       │ 实现端口
┌──────────────────────▼──────────────────────────────┐
│  infrastructure (技术实现)                            │
│  mysql  redisx  storage  mcp  einoorch  config      │
└─────────────────────────────────────────────────────┘
```

**核心规则**：domain 层只能定义接口（Port），不能引用 infrastructure 包。
所有实现依赖通过依赖注入从外部传入。

### 双引擎编排

- **Native Loop**：自研 ReAct 循环，精确控制每一步
- **Eino Graph**：基于 CloudWeGo Eino 框架的图编排，支持复杂 DAG
- **切换方式**：通过 `config.yaml` 中的 `orchestrator.type` 配置

### 安全优先

所有工具调用必须经过 5 层权限闸门：
1. **L1** 命令黑名单（rm -rf, dd, fork bomb, force push）
2. **L2** 路径沙箱 + 编码绕过防护
3. **L3** 工具分级（read=allow, write=confirm, bash=confirm, MCP=confirm）
4. **L4** 会话级放行
5. **L5** 拒绝熔断（连续 5 次拒绝后自动熔断）

## 代码规范

### 命名约定

- 包名：小写单词，无下划线（`agent`、`subagent`、`einoorch`）
- 接口：domain 层的端口接口用 `I` 前缀（`ILLMPort`、`IMessageRepository`）
- 结构体：驼峰（`SecurityGuard` → 实际为 `Guard`，简洁命名）
- 方法：动词开头（`LoadSpec`、`MarkTaskDone`、`CheckPermission`）
- 常量：驼峰或全大写（`DefaultMaxRounds`、`DefaultTokenBudget`）

### 文件组织

```
internal/
├── domain/           # 业务核心（纯逻辑，无框架依赖）
│   ├── agent/        # Agent 引擎、ReAct 循环、计划
│   ├── security/     # 权限闸门、路径沙箱
│   ├── tool/         # 工具定义和注册
│   ├── skill/        # Skill 技能包系统
│   ├── mcp/          # MCP 协议模型和端口
│   ├── memory/       # 跨会话记忆
│   ├── subagent/     # 子 Agent 执行器
│   ├── team/         # Agent Teams 协同
│   ├── hook/         # 生命周期 Hook 事件
│   ├── slash/        # Slash Commands
│   ├── contextx/     # 上下文压缩
│   ├── spec/         # Spec 驱动开发
│   ├── telemetry/    # 可观测性接口
│   └── audit/        # 审计日志
├── infrastructure/   # 技术实现（数据库、LLM、MCP 等）
│   ├── mysql/        # MySQL 存储实现
│   ├── redisx/       # Redis 缓存实现
│   ├── storage/      # 文件存储
│   ├── mcp/          # MCP stdio 客户端
│   ├── einoorch/     # Eino 编排引擎
│   └── config/       # 配置加载
├── adapters/         # 适配器（API 层）
│   ├── http/         # HTTP Server
│   └── ws/           # WebSocket
└── bootstrap/        # 应用装配（DI 根）
```

### 错误处理

- **禁止静默吞噬错误**：`_ = err` 改为 `log.Printf` + 显式处理
- **工具执行错误**：返回 `tool.Result{Text: err.Error(), IsError: true}`
- **LLM 错误**：返回结构化错误码（`llm`、`budget`、`cancel`）
- **权限拒绝**：返回用户可见的拒绝原因，不要吞掉

### 日志规范

- 日志前缀：`[spec]`、`[mcp]`、`[hook]`、`[agent]`、`[security]`
- 关键操作：info 级别
- 错误/警告：warn/error 级别，附带上下文
- 不要在循环中打高频日志（会影响性能）

### 测试规范

- 每个 domain 包必须有 `_test.go`
- 使用表驱动测试（table-driven tests）
- 安全测试必须覆盖边界情况（编码绕过、空格变体、分号注入）
- 工具测试验证输入/输出格式

## 开发流程

### 启动开发

```bash
# 克隆项目
git clone <repo-url>
cd code-agent

# 安装依赖
go mod download

# 运行测试
go test ./internal/...

# 本地启动（使用 Mock LLM）
go run ./cmd/server --config configs/config.yaml

# 真实模型启动
LLM_USE_MOCK=false LLM_API_KEY=xxx go run ./cmd/server
```

### Docker 部署

```bash
# 复制环境变量模板
cp .env.example .env

# 一键启动所有服务（MySQL + Redis + MinIO + Jaeger + Server）
docker compose --profile app up -d

# 开发模式（带 bash/curl 的调试镜像）
docker compose --profile dev up -d

# 查看服务状态
docker compose ps

# 查看日志
docker compose logs -f server
```

### Git 提交规范

```
feat: 新功能
fix: Bug 修复
refactor: 重构
docs: 文档
test: 测试
perf: 性能优化
chore: 构建/工具
```

### Spec 驱动开发流程

1. **编写 spec.md**：定义目标、约束、验收标准
2. **编写 tasks.md**：拆分子任务，标记完成状态
3. **编写 checklist.md**：验收检查清单
4. **将三个文件放在项目根目录**
5. **Agent 自动加载**：在 system prompt 中注入 spec 内容
6. **进度自动追踪**：完成任务时自动更新 tasks.md 和 checklist.md

## 安全红线

- **禁止** 在没有用户确认的情况下执行 `bash`、`write_file`、`edit_file`
- **禁止** 让工具执行跨越工作区目录的操作
- **禁止** 在日志中打印敏感信息（API Key、密码、Token）
- **禁止** 绕过权限闸门直接调用工具
- **禁止** 在 domain 层引用 infrastructure 包

## 面试亮点话术

**Q: 这和 Claude Code / Cursor 有什么区别？**
A: "Claude Code 是重要的灵感来源，但 Code-Agent 有几个独特之处：
1. **纯 Go 实现**——适合后端服务部署，不依赖 Node.js/Electron
2. **六边形架构**——LLM、MCP、存储都可以替换，domain 层零依赖
3. **双引擎编排**——自研 Loop + Eino 框架可配置切换
4. **5 层安全闸门**——从命令黑名单到熔断的纵深防御，这在开源 Agent 中很少见
5. **多 Agent 协同**——Worktree 隔离 + 4 种协同模式（parallel/review/debate/merge）"

**Q: 你的上下文压缩是怎么做的？**
A: "我实现了 L0-L3 四级压缩策略：
- L0：单条消息截断，零成本
- L1：优先级保留，错误消息/高优先级不压缩
- L2：仅保留最近 N 条
- L3：LLM 生成摘要，保留目标/决策/文件路径/错误/下一步
同时有 TokenManager 实时预算监测，到达阈值时紧急 mid-loop 修剪。"

**Q: MCP 是怎么接入的？**
A: "完整实现了 JSON-RPC 2.0 协议的 stdio 客户端，支持 initialize/tools/list/tools/call 三个核心方法。
Manager 负责多服务器生命周期管理，Watchdog 自动重连崩溃的服务器，
ToolBridge 自动将 MCP 工具同步到 Agent 工具注册表，经过权限闸门检查后才能调用。"
