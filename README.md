# Code-Agent

类 **Claude Code** 的 Coding Agent 运行时（Go）：**Eino 负责编排与对话，自研负责安全执行与产品层**。

- 仓库：`git@github.com:spray272598/code-agent.git`
- 设计：[docs/design.md](docs/design.md) · 边界：[docs/boundary.md](docs/boundary.md) · Eino：[docs/eino-integration.md](docs/eino-integration.md) · 面试：[docs/interview-guide.md](docs/interview-guide.md)

## 架构原则（必读）

| Eino（框架） | 自研（护城河） |
|--------------|----------------|
| ChatModel / Stream | HTTP SSE、心跳、鉴权、CORS |
| ReAct / MultiAgent 图 | Guard 五层权限 + 归一化 |
| tool-calling 协议 | GuardedTool 横切（validate/hook/cache/audit） |
| — | Workspace 六工具 + bash 进程隔离 |
| MCP 可对接 | MCP 热装 + **一律过 Guard**（`server__tool`） |
| — | Skill / Memory / 会话持久化 / CLI |

```
CLI ──SSE──► Server(trigger)
               └─ ChatApp
                    └─ Runner: Eino（主）| native-offline（mock）
                         └─ GuardedTool → domain tools + MCP
```

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

## 能力清单

- **编排**：Eino ReAct + callbacks→SSE；`/team` 并行子代理；native 自研 Loop 兜底  
- **安全**：五层 Guard、路径/命令归一化、HITL、Hook abort、审计、Redis 限流  
- **工具**：read/write/edit/bash/glob/grep + memory + delegate  
- **MCP**：stdio 热装，`server__tool` 注册，**与 core 工具同一 GuardedTool 横切**  
- **Skill / 记忆 / L0–L3 压缩 / Token 预算**  
- **SQLite | MySQL | memory**；MinIO；OTLP/Prometheus；host-agent  

## 文档

| 文档 | 内容 |
|------|------|
| [docs/boundary.md](docs/boundary.md) | **Eino vs 自研边界** |
| [docs/eino-integration.md](docs/eino-integration.md) | GuardedTool / 压缩 / 预算 |
| [docs/architecture.md](docs/architecture.md) | Ports & Adapters |
| [docs/agent-loop.md](docs/agent-loop.md) | ReAct 流程 |
| [docs/mcp.md](docs/mcp.md) | MCP 对接 |
| [docs/interview-guide.md](docs/interview-guide.md) | 秋招话术 |
| [docs/design.md](docs/design.md) | 总体设计 |

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
