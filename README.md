# Code-Agent

A **Claude Code-like** Coding Agent runtime (Go): **Eino handles orchestration and dialogue, while custom code handles security execution and product layers**.

- Design: [docs/design.md](docs/design.md) · Boundaries: [docs/boundary.md](docs/boundary.md) · Eino: [docs/eino-integration.md](docs/eino-integration.md)

## Architecture Principles (Must Read)

| Eino (Framework) | Custom (Moat) |
|------------------|---------------|
| ChatModel / Stream | HTTP SSE, heartbeat, auth, CORS |
| ReAct / MultiAgent graph | Guard 5-layer permission + normalization |
| tool-calling protocol | GuardedTool cross-cutting (validate/hook/cache/audit) |
| — | Workspace 6 tools + bash process isolation |
| MCP integration | MCP hot-swap + **all through Guard** (`server__tool`) |
| — | Skill / Memory / Session persistence / CLI |
| — | **LLM retry classifier** (pure function, 429/5xx auto-retry, 400 context overflow triggers compression) |
| — | **Tool per-path write lock parallelism** (write same file serial, different files/read fully parallel) |
| — | **Stall detection two-level** (nudge self-correction → hard stop) |

```
CLI ──SSE──► Server(trigger)
               └─ ChatApp
                    └─ Runner: Eino (primary) | native-offline (mock)
                         └─ GuardedTool → domain tools + MCP
```

### Standard Protocol Layer

```
External clients (VS Code, Claude Desktop, IDE)
    │
    ├─ MCP (Streamable HTTP / stdio) ──► MCPServer ──► ToolRegistry
    ├─ ACP (JSON-RPC 2.0) ────────────► ACPHandler ──► ChatApp
    └─ JSON-RPC 2.0 ──────────────────► jsonrpc.Server
```

## Directory Structure (DDD Layering)

Code is located in `internal/`, strictly layered by Domain-Driven Design, with dependencies flowing top-down (outer layers depend on inner layers; domain layer has no outer dependencies):

```
cmd/                      # Entry layer: server / cli / host-agent process bootstrap
internal/
  domain/                 # Domain layer (core business, no external dependencies)
    agent/                #   engine (main loop/control) · plan (visualization/replanning) · events
    contextx/             #   context compression Compressor / Summarizer
    memory/               #   long-term memory (vector recall + solidification)
    security/             #   Guard 5-layer permission · sandbox 3 modes (readonly/workspace/strict)
    subagent/             #   sub-agent orchestration · window isolation writeback
    session/              #   session model and persistence repository
    tool/                 #   domain tools (coding/workspace/ssh/mcp…)
    intent/ · model/ · deepagent/ · checkpoint/ · audit/
  application/            # Application layer: use case orchestration (ChatApp / RunBackground / Options)
  infrastructure/         # Infrastructure layer: external adapters
    einoorch/             #   Eino orchestration Runner (async compression, sub-agent injection)
    jsonrpc/              #   JSON-RPC 2.0 core transport (MCP Server / ACP shared)
    config/ · llm/ · mcp/ · redis/ · mysql/ · sqlite/ · kms/ · vector/ · ssh/
  trigger/                # Trigger layer: HTTP(SSE) · MCP · ACP adapters
  bootstrap/              # Composition root: wire all layer dependencies, inject configuration
web/                      # Frontend (Vite + React, independent build, output web/dist/ not in repo)
docs/                     # Design / architecture / boundaries / roadmap documentation
configs/                  # Runtime configuration (config.yaml)
scripts/                  # Local one-click / evaluation / stress test (PowerShell)
commands/ hooks/ skills/ teams/ deploy/   # Prompts / hooks / skills / team orchestration / deployment manifests
```

> Runtime artifacts (binaries, /bin, /tmp, /data, /secrets, /workspace, /reports, web/dist, *.tsbuildinfo) are excluded by `.gitignore` and not committed.

## Quick Start

```powershell
# One-click trial (mock, auto-starts server + CLI)
powershell -File scripts/try_cli.ps1

# Smoke test only (non-interactive)
powershell -File scripts/try_cli.ps1 -SmokeOnly

# Manual zero-dependency:
go run ./cmd/server -config configs/config.yaml
# Another terminal
go run ./cmd/cli --base http://127.0.0.1:8080 --key dev-key
```

### Production/Real Model (Eino Primary Path)

```bash
# PowerShell
$env:LLM_API_KEY="sk-..."
$env:LLM_API_BASE="https://api.siliconflow.cn/v1"   # or OpenAI compatible
$env:LLM_MODEL="deepseek-ai/DeepSeek-V3"
$env:LLM_USE_MOCK="false"
# Optional: $env:AGENT_ORCHESTRATOR="eino"   # default is already eino
go run ./cmd/server -config configs/config.yaml
```

```yaml
# configs/config.yaml
agent:
  orchestrator: eino        # primary path; auto native-offline when no key/mock
  eino_stream: false
  token_budget: 32000
llm:
  use_mock: false
  api_key: "..."            # or environment variable
```

## CLI Interaction

| Command | Effect |
|---------|--------|
| Regular input | Multi-turn dialogue (SSE) |
| `/pending` | List pending permission requests |
| `/approve [id] [once\|session]` | Approve and **inline continue** |
| `y` / `/continue` | Confirm and continue after approval |
| `/tools` `/mcp` `/help` | List and help |
| `/team …` | Eino multi-agent explore+verify (eino mode) |

## Account & Auth (toC)

面向个人用户（**无企业/组织概念**），数据统一按 `user_id` 隔离：

- 邮箱 + 密码注册 / 登录，密码 bcrypt 哈希；JWT（`access_token` + `refresh_token`）鉴权
- 邮箱验证（注册激活）、密码重置（邮件链接）
- 连接管理、记忆、SSH 资源等全部以 `user_id` 为边界，无 `org_id`
- 详见 [docs/design.md](docs/design.md)；本地一键见 [docs/local-demo.md](docs/local-demo.md)

## Capabilities

- **Account (toC)**: Email + password registration/login, JWT auth, email verification and password reset; data isolated by `user_id` (no organization concept)
- **Orchestration**: Eino ReAct + callbacks→SSE; `/team` parallel sub-agents; native Loop fallback; **plan-execute-reflect** visualization + interruptible replanning (3.5)
- **Security**: 5-layer Guard, path/command normalization, HITL, Hook abort, audit, Redis rate limiting; **sandbox 3 modes** (readonly / workspace / strict, 5.1)
- **LLM Reliability**: Pure function retry classifier `ClassifyLLMError` (21 table-driven unit tests); 429 exponential backoff±20% jitter respecting Retry-After; 400 context overflow→compress then resubmit (`ErrContextOverflow`); 401/403 propagate to auth layer (`ErrAuth`)
- **Tool Parallelism**: Per-path write lock replacing allRead binary split—tool calls writing same file serialize via `locks[path]`, different files/reads run fully parallel; bash uses global mutex
- **Stall Detection**: Loop consecutive duplicate tool signature → `same==1` injects nudge prompt for model self-correction, `same>=3` hard stop with reflection + error
- **Context Safety**: `SelectSafeSplit` with `min_compactable` lower bound (skip LLM summarization when compactable zone too small) + snap protection
- **Tools (Local Workspace)**: read/write/edit/bash/glob/grep + `apply_patch` (structured diff) + `lint`/`codecov` + `memory` + `delegate` (5.2)
- **Tools (Remote SSH)**: `ssh_exec` / `ssh_read_file` / `ssh_write_file` / `ssh_list_dir` / `ssh_terminal` (interactive PTY); connection credentials encrypted via KMS
- **MCP**: stdio/HTTP hot-swap, `server__tool` registration, **same GuardedTool cross-cutting as core tools**; **MCP Server** exposes tools/resources/prompts to external clients
- **Standard Protocols**: JSON-RPC 2.0 core transport; MCP Server (provider) + MCP Client (consumer); ACP over JSON-RPC 2.0 (IDE integration)
- **Context Management**: Async compression (configurable threshold `compact_threshold_ratio`) + long-task cross-segment memory solidification + PlanMode exploration isolation + sub-agent window isolation writeback
- **Ecosystem**: Usage monitoring dashboard `/api/v1/usage`; Plan read-only exploration state machine; Headless background long tasks
- **Skill / Memory / L0–L3 Compression / Token Budget**
- **Storage/Observability**: SQLite | MySQL | memory; MinIO; OTLP/Prometheus; host-agent
- **CI/CD**: golangci-lint v2 + gofumpt formatting gate; CI 3-shard parallel testing + 10-minute timeout per shard; coverage auto-merge report

## Documentation

| Document | Content |
|----------|---------|
| [docs/boundary.md](docs/boundary.md) | **Eino vs Custom boundaries** |
| [docs/eino-integration.md](docs/eino-integration.md) | GuardedTool / compression / budget |
| [docs/architecture.md](docs/architecture.md) | Ports & Adapters |
| [docs/agent-loop.md](docs/agent-loop.md) | ReAct flow |
| [docs/mcp.md](docs/mcp.md) | MCP integration (Client + Server) |
| [docs/design.md](docs/design.md) | Overall design |
| [docs/roadmap.md](docs/roadmap.md) | **toC product and engineering roadmap / future work planning** |
| [docs/learning-guide.md](docs/learning-guide.md) | Learning guide |

## Local One-Click (Host + Server)

```powershell
# mock + prefer_host: tools can execute in local workspace
powershell -File scripts/dev_local.ps1 -Workspace .

# Real LLM (Eino primary path)
$env:LLM_API_KEY="sk-..."
$env:LLM_USE_MOCK="false"
powershell -File scripts/dev_local.ps1 -RealLLM -Workspace D:\your\repo

# Another terminal
.\bin\cli.exe --key dev-key
powershell -File scripts/eval_report.ps1   # → reports/eval-latest.json + .md
```

See [docs/local-demo.md](docs/local-demo.md).

## Evaluation / Docker / Real Model Stress Test

```powershell
# Lightweight smoke test
powershell -File scripts/eval_smoke.ps1
# Numeric report (pass_rate / per-case latency)
powershell -File scripts/eval_report.ps1

# Mock stress test evidence (no API Key, produces design verification baseline)
powershell -File scripts/mock_stress.ps1
# → reports/mock-stress-latest.json + .md

# Real model long-task stress test (Key only via environment variable, never commit to repo)
$env:LLM_API_KEY="sk-..."
$env:LLM_BASE_URL="https://api.siliconflow.cn/v1"
$env:LLM_MODEL="Qwen/Qwen2.5-32B-Instruct"
$env:LLM_USE_MOCK="false"
powershell -File scripts/llm_stress.ps1
# → reports/llm-stress-latest.json + .md
```

Documentation: [checkpoint-index.md](docs/checkpoint-index.md) · [deepagent-vs-teams.md](docs/deepagent-vs-teams.md) · [eino-integration.md](docs/eino-integration.md)

```powershell
# Docker middleware
docker compose up -d
# Docker full stack (including server image)
docker compose --profile app up -d --build
# See scripts/docker-up.md · Dockerfile
```

## License

MIT
