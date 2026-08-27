# Code-Agent

类 **Claude Code** 的 Coding Agent 运行时（Go）：**Eino 负责编排与对话，自研负责安全执行与产品层**。

- 设计：[docs/design.md](docs/design.md) · 边界：[docs/boundary.md](docs/boundary.md) · Eino：[docs/eino-integration.md](docs/eino-integration.md)

## 架构原则（必读）

| Eino（框架） | 自研（护城河） |
|--------------|----------------|
| ChatModel / Stream | HTTP SSE、心跳、鉴权、CORS |
| ReAct / MultiAgent 图 | Guard 五层权限 + 归一化 |
| tool-calling 协议 | GuardedTool 横切（validate/hook/cache/audit） |
| — | Workspace 六工具 + bash 进程隔离 |
| MCP 可对接 | MCP 热装 + **一律过 Guard**（`server__tool`） |
| — | Skill / Memory / 会话持久化 / CLI |
| — | **LLM 重试分类器**（纯函数，429/5xx 自动重试，400 上下文溢出触发压缩） |
| — | **工具 per-path 写锁并行**（写同文件串行，异文件/读完全并行） |
| — | **停滞检测两级**（nudge 自纠 → 硬停） |

```
CLI ──SSE──► Server(trigger)
               └─ ChatApp
                    └─ Runner: Eino（主）| native-offline（mock）
                         └─ GuardedTool → domain tools + MCP
```

### 标准协议层

```
外部客户端 (VS Code, Claude Desktop, IDE)
    │
    ├─ MCP (Streamable HTTP / stdio) ──► MCPServer ──► ToolRegistry
    ├─ ACP (JSON-RPC 2.0) ────────────► ACPHandler ──► ChatApp
    └─ JSON-RPC 2.0 ──────────────────► jsonrpc.Server
```

## 目录结构（DDD 分层）

代码位于 `internal/`，按领域驱动设计严格分层，依赖方向自上而下（外层依赖内层，领域层不依赖任何外层）：

```
cmd/                      # 入口层：server / cli / host-agent 进程引导
internal/
  domain/                 # 领域层（核心业务，无外部依赖）
    agent/                #   engine(主循环/控制) · plan(可视化/重规划) · events
    contextx/             #   上下文压缩 Compressor / Summarizer
    memory/               #   长期记忆（向量召回 + 固化）
    security/             #   Guard 五层权限 · sandbox 三档（readonly/workspace/strict）
    subagent/             #   子代理编排 · 窗口隔离回写
    session/              #   会话模型与持久化仓储
    tool/                 #   领域工具（coding/workspace/ssh/mcp…）
    intent/ · model/ · deepagent/ · checkpoint/ · audit/
  application/            # 应用层：用例编排（ChatApp / RunBackground / Options）
  infrastructure/         # 基础设施层：外部适配器
    einoorch/             #   Eino 编排 Runner（异步压缩、子代理注入）
    jsonrpc/              #   JSON-RPC 2.0 核心传输（MCP Server / ACP 共用）
    config/ · llm/ · mcp/ · redis/ · mysql/ · sqlite/ · kms/ · vector/ · ssh/
  trigger/                # 触发层：HTTP(SSE) · MCP · ACP 适配
  bootstrap/              # 组合根：装配各层依赖、注入配置
web/                      # 前端（Vite + React，独立构建，产物 web/dist/ 不入库）
docs/                     # 设计/架构/边界/路线图等文档
configs/                  # 运行时配置（config.yaml）
scripts/                  # 本地一键 / 评测 / 压测（PowerShell）
commands/ hooks/ skills/ teams/ deploy/   # 提示词 / 钩子 / 技能 / 团队编排 / 部署清单
```

> 运行时产物（二进制、/bin、/tmp、/data、/secrets、/workspace、/reports、web/dist、*.tsbuildinfo）均已由 `.gitignore` 排除，不入库。

## 快速开始

```powershell
# 一键试用（mock，自动起 server + CLI）
powershell -File scripts/try_cli.ps1

# 仅冒烟（非交互）
powershell -File scripts/try_cli.ps1 -SmokeOnly

# 零依赖手动：
go run ./cmd/server -config configs/config.yaml
# 另一终端
go run ./cmd/cli --base http://127.0.0.1:8080 --key dev-key
```

### 生产/真实模型（Eino 主路径）

```bash
# PowerShell
$env:LLM_API_KEY="sk-..."
$env:LLM_API_BASE="https://api.siliconflow.cn/v1"   # 或 OpenAI 兼容
$env:LLM_MODEL="deepseek-ai/DeepSeek-V3"
$env:LLM_USE_MOCK="false"
# 可选：$env:AGENT_ORCHESTRATOR="eino"   # 默认已是 eino
go run ./cmd/server -config configs/config.yaml
```

```yaml
# configs/config.yaml
agent:
  orchestrator: eino        # 主路径；无 key/mock 时自动 native-offline
  eino_stream: false
  token_budget: 32000
llm:
  use_mock: false
  api_key: "..."            # 或环境变量
```

## CLI 交互

| 命令 | 作用 |
|------|------|
| 普通输入 | 多轮对话（SSE） |
| `/pending` | 列出待确认权限 |
| `/approve [id] [once\|session]` | 批准并 **inline continue** |
| `y` / `/continue` | 确认后继续 |
| `/tools` `/mcp` `/help` | 列表与帮助 |
| `/team …` | Eino 多代理 explore+verify（eino 模式） |

## 账号与鉴权（toC）

面向个人用户（**无企业/组织概念**），数据统一按 `user_id` 隔离：

- 邮箱 + 密码注册 / 登录，密码 bcrypt 哈希；JWT（`access_token` + `refresh_token`）鉴权
- 邮箱验证（注册激活）、密码重置（邮件链接）
- 连接管理、记忆、SSH 资源等全部以 `user_id` 为边界，无 `org_id`
- 详见 [docs/design.md](docs/design.md)；本地一键见 [docs/local-demo.md](docs/local-demo.md)

## 能力清单

- **账号（toC）**：邮箱 + 密码注册/登录，JWT 鉴权，邮箱验证与密码重置；数据按 `user_id` 隔离（无企业/组织概念）
- **编排**：Eino ReAct + callbacks→SSE；`/team` 并行子代理；native 自研 Loop 兜底；**计划-执行-反思**可视化 + 可中断重规划（3.5）
- **安全**：五层 Guard、路径/命令归一化、HITL、Hook abort、审计、Redis 限流；**sandbox 三档**（readonly / workspace / strict，5.1）
- **LLM 可靠性**：纯函数重试分类器 `ClassifyLLMError`（21 个表驱动单测）；429 指数退避±20% 抖动尊重 Retry-After；400 上下文溢出→压缩后重提交（`ErrContextOverflow`）；401/403 上抛鉴权层（`ErrAuth`）
- **工具并行**：per-path 写锁替代 allRead 二分法——写同一文件的工具调用通过 `locks[path]` 互斥串行，写不同文件 / 读操作完全并行；bash 用全局互斥
- **停滞检测**：循环内连续重复工具签名 → `same==1` 注入 nudge 提示词给模型自纠机会，`same>=3` 硬停反射+报错
- **上下文安全**：`SelectSafeSplit` 加 `min_compactable` 下限（可压缩区 token 过少时不浪费 LLM 摘要调用）+ snap 保护
- **工具（本地 Workspace）**：read/write/edit/bash/glob/grep + `apply_patch`（结构化 diff）+ `lint`/`codecov` + `memory` + `delegate`（5.2）
- **工具（远程 SSH）**：`ssh_exec` / `ssh_read_file` / `ssh_write_file` / `ssh_list_dir` / `ssh_terminal`（交互式 PTY 终端）；连接凭据经 KMS 加密存储
- **MCP**：stdio/HTTP 热装，`server__tool` 注册，**与 core 工具同一 GuardedTool 横切**；**MCP Server** 暴露 tools/resources/prompts 给外部客户端
- **标准协议**：JSON-RPC 2.0 核心传输层；MCP Server（provider）+ MCP Client（consumer）；ACP over JSON-RPC 2.0（IDE 集成）
- **上下文管理**：异步压缩（阈值可配 `compact_threshold_ratio`）+ 长任务跨段记忆固化 + PlanMode 探索期上下文隔离 + 子代理窗口隔离回写
- **生态对接**：用量监控面板 `/api/v1/usage`；Plan 只读探索期状态机；Headless 后台长任务
- **Skill / 记忆 / L0–L3 压缩 / Token 预算**
- **存储/可观测**：SQLite | MySQL | memory；MinIO；OTLP/Prometheus；host-agent 
- **CI/CD**：golangci-lint v2 + gofumpt 格式化门禁；CI 3 分片并行测试 + 每分片 10 分钟超时；覆盖率自动合并报告

## 文档

| 文档 | 内容 |
|------|------|
| [docs/boundary.md](docs/boundary.md) | **Eino vs 自研边界** |
| [docs/eino-integration.md](docs/eino-integration.md) | GuardedTool / 压缩 / 预算 |
| [docs/architecture.md](docs/architecture.md) | Ports & Adapters |
| [docs/agent-loop.md](docs/agent-loop.md) | ReAct 流程 |
| [docs/mcp.md](docs/mcp.md) | MCP 对接（Client + Server） |
| [docs/design.md](docs/design.md) | 总体设计 |
| [docs/roadmap.md](docs/roadmap.md) | **toC 产品与工程路线图 / 后续工作规划** |
| [docs/learning-guide.md](docs/learning-guide.md) | 学习指导 |

## 本机一键（Host + Server）

```powershell
# mock + prefer_host：工具可在本机 workspace 执行
powershell -File scripts/dev_local.ps1 -Workspace .

# 真实 LLM（Eino 主路径）
$env:LLM_API_KEY="sk-..."
$env:LLM_USE_MOCK="false"
powershell -File scripts/dev_local.ps1 -RealLLM -Workspace D:\your\repo

# 另开终端
.\bin\cli.exe --key dev-key
powershell -File scripts/eval_report.ps1   # → reports/eval-latest.json + .md
```

详见 [docs/local-demo.md](docs/local-demo.md)。

## 评测 / Docker / 真模型压测

```powershell
# 轻量冒烟
powershell -File scripts/eval_smoke.ps1
# 数字报告（pass_rate / 每 case 延迟）
powershell -File scripts/eval_report.ps1

# Mock 压测证据（无 API Key，产出 design 验收基线）
powershell -File scripts/mock_stress.ps1
# → reports/mock-stress-latest.json + .md

# 真模型长任务压测（Key 只走环境变量，勿写入仓库）
$env:LLM_API_KEY="sk-..."
$env:LLM_BASE_URL="https://api.siliconflow.cn/v1"
$env:LLM_MODEL="Qwen/Qwen2.5-32B-Instruct"
$env:LLM_USE_MOCK="false"
powershell -File scripts/llm_stress.ps1
# → reports/llm-stress-latest.json + .md
```

文档：[checkpoint-index.md](docs/checkpoint-index.md) · [deepagent-vs-teams.md](docs/deepagent-vs-teams.md) · [eino-integration.md](docs/eino-integration.md)

```powershell
# Docker 中间件
docker compose up -d
# Docker 全栈（含 server 镜像）
docker compose --profile app up -d --build
# 详见 scripts/docker-up.md · Dockerfile
```

## License

MIT
