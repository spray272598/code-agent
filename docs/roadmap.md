# Code-Agent 路线图与后续工作规划（开源单 operator Agent Harness）

> **方向**：开源、单 operator 的 Coding Agent 运行时；**无账号 / 多租户 / 组织概念**，鉴权为 API-Key；所有资源按 operator（运行身份）边界管理。
> **目的**：对齐产品定位、沉淀已落地能力、规划后续迭代。本仓库已从 toC SaaS 改造为开源 harness：
> - Phase 1 移除账号系统 / 多租户 / LLM Key 域，改为单 operator（API-Key 鉴权）；
> - Phase 2 将自研 Native Loop 设为主编排器，Eino 降为可选后端（无真实 Key 时诚实降级到 Native）；
> - Phase 3 内核沙箱诚实降级（`EnforcementLevel` 三档 + `require_kernel_sandbox` fail-closed）；
> - Phase 4 清理死代码，并将 `GoalOrchestrator`（`goal_*`）标记为 experimental。
> **更新**：2026-09-05

---

## 1. 当前已落地能力（基线）

| 域 | 状态 | 说明 |
|----|------|------|
| **鉴权** | ✅ 已落地（重构后） | 单 operator，API-Key 鉴权（Server 校验请求头 `X-API-Key`）；无账号 / 密码 / JWT / 多租户。详见 [design.md §5.3](./design.md) |
| **编排** | ✅ 已落地 | **Native Loop 主路径（自研）** + Eino 可选后端（`orchestrator: eino` + 真实 Key，否则诚实降级到 Native）；`/team` 并行子代理；SSE 流式 |
| **安全** | ✅ 已落地 | 五层 Guard、路径/命令归一化、HITL、Hook abort、审计、Redis 限流；沙箱三档（readonly / workspace / strict）诚实降级 |
| **本地工具** | ✅ 已落地 | read/write/edit/bash/glob/grep + memory + delegate；进程隔离 |
| **远程 SSH** | ✅ 已落地 | `ssh_exec` / `ssh_read_file` / `ssh_write_file` / `ssh_list_dir` / **`ssh_terminal`（交互式 PTY 终端）**；连接凭据 KMS 加密存储 |
| **SSH 实时终端** | ✅ 已落地 | 后端 `ws.SSHTerminalHub`（WebSocket 代理 PTY），前端 xterm.js 终端页 `/ssh-terminal`；`ITerminal.Read` 异步缓冲（已补单测） |
| **SSH 连接管理 UI** | ✅ 已落地 | 前端 `/ssh-connections`：列表/新增/删除，KMS 加密存储 |
| **意图识别** | ✅ 已落地 | 规则 + LLM 兜底；**跨轮指代消解**（`EntityContext` + `ExtractEntities`，解析"那台机器/刚才那个文件"等），已接入 runner |
| **MCP** | ✅ 已落地 | stdio 热装，`server__tool` 注册，与 core 工具同一 GuardedTool 横切 |
| **代码 RAG（向量检索）** | ✅ 已落地 | `code_search` 关键词 + 文件级语义 + **分块级 RAG**（chunk 向量索引）；向量后端默认 `MemIndex`，可切 Qdrant（`vector.provider=qdrant`） |
| **记忆/压缩** | ✅ 已落地 | Skill / 长期记忆 / L0–L3 压缩 / Token 预算 |
| **前端** | ✅ 已落地 | React/TS SPA（web/）：对话 + SSH 连接管理 UI；账号流已在单 operator 化中移除，改为 API-Key 编辑器 |
| **可观测/存储** | ✅ 已落地 | OTLP/Prometheus、MinIO、host-agent |

> 与设计稿、README 对齐：[docs/design.md](docs/design.md) · [README.md](../README.md)

---

## 2. 相对专项工具的短板（需提升）

基于与同类项目（如 Java 系 SSH 运维工具）的对比，我们**鉴定并补齐**了以下差距：

1. **交互式终端**：已在 `internal/infrastructure/ssh/terminal.go` 实现异步缓冲读取，新增 `ssh_terminal` 工具，并补齐 `terminal_test.go`；WebSocket 实时终端（`ws.SSHTerminalHub` + 前端 xterm.js）已落地。
   - *待提升*：会话持久化、多会话标签。
2. **意图识别深度**：当前为「规则 + LLM 兜底」路由（continue/deep/team/normal），跨轮指代消解已落地。
3. **编排成熟度**：Native Loop 已够用；相比 Google ADK 类编排框架，缺更结构化的"计划-执行-反思"可视化与可中断重规划（见 3.5）。
4. **文档时效**：已随 Phase 1–4 修订 design.md / README.md / 本路线图，将 toC / 多租户 / 账号 陈旧描述替换为单 operator 开源 harness 事实。

---

## 3. 后续工作规划（按优先级）

### P1 — 近期（产品可用性与体验闭环）【已完成 ✅】

| # | 任务 | 关联代码 | 状态 |
|---|------|----------|------|
| 1.1 | **WebSocket 实时终端**：`ws.SSHTerminalHub` 代理 PTY；前端 xterm.js 终端页 `/ssh-terminal` | `trigger/ws/ssh_terminal_hub.go`、`web/src/pages/SSHTerminal.tsx` | ✅ |
| 1.2 | **意图指代消解**：`EntityContext` + `ExtractEntities`，解析"那台机器/刚才那个文件" | `domain/intent/classifier.go`、接入 `einoorch/runner.go` | ✅ |
| 1.3 | **Web API-Key 配置 UI**：侧栏账号流已随单 operator 化移除，改为 API-Key 编辑器 | `web/src/*` | ✅ |
| 1.4 | **SSH 连接管理 UI**：`/ssh-connections` 列表/新增/删除 | `web/src/pages/SSHConnections.tsx` | ✅ |

### P2 — 中期（能力深化与质量）

| # | 任务 | 关联代码 | 价值 |
|---|------|----------|------|
| 2.1 | **代码 RAG / Qdrant**：仓库向量索引 + 检索增强生成，提升跨文件理解 | `internal/infrastructure/vector/qdrant`（Qdrant 适配，`IVectorIndex`）、`codeindex` 分块级 `SearchSemanticChunks` + `SetVectorIndex`、配置 `vector.provider=qdrant` | ✅ |
| 2.2 | **自动化评测体系**：意图路由 / 指代消解回归用例 + 评估脚本，防回归 | `scripts/eval/fixtures.yaml`（数据驱动数据集）+ `internal/domain/intent/regression_test.go`（路由/LLM 兜底/指代消解断言）+ `scripts/eval/run_eval.ps1`（build+vet+回归一站式，CI 友好） | ✅ |
| 2.3 | **TUI 终端**：本地终端界面（纯标准库，离线/本机场景），凭据本地加密（KMS 装饰 repo）、`/conn` 管理 + 跨设备撤销 | `cmd/tui/`（交互 REPL + `app.go` 命令分发 + `app_test.go`）、复用 `EncryptingConnRepo` | ✅ |
| 2.4 | **MCP SDK 评估**：在 `IMCPClient` 接口下评估 `mark3labs/mcp-go` 等 SDK（触发：接 >20 个 server 或需 resources/prompts/sampling） | `docs/mcp-sdk-eval.md`（结论；适配 spike 已在 Phase 4 清理） | ✅ |

### P3 — 远期（规模化与生态）

| # | 任务 | 价值 |
|---|------|------|
| 3.1 | 多模型路由 + 成本优化（按意图 normal/deep/team 选模型、凭据继承、可用性回退；Token/成本预算追踪，`internal/domain/model`） | ✅ 降本 |
| 3.2 | 技能市场 / 自定义 Skill 上传（Marketplace 接口 + LocalMarketplace 目录化、SearchMarket 搜索含安装态、UploadSkill 自定义上传校验、InstallListing 从市场安装） | ✅ 生态 |
| 3.3 | 可观测看板（Prometheus `/metrics` + JSON 端点已挂载；`AddQuotaDeny` 配额拒绝指标）+ **operator 级**每日 Token 配额强制（`checkQuota` + `TokenQuota` 配置 + `WithTokenQuota`） | ✅ 运营 |
| 3.4 | SSO / 第三方登录（可选扩展）：原 `auth/oauth.go`（OAuth2 授权码 + PKCE）已在单 operator 化中随账号域一并移除；当前鉴权为 API-Key。如需 GitHub/Google SSO，作为后续可选后端，不进入默认路径 | 可选 |
| 3.5 | 计划-执行-反思可视化与可中断重规划（编排增强）：`Plan.View()` 结构化进度快照 + `Visualize()` ASCII 树供 UI 渲染；`EventPlanUpdate` 每步增量进度广播；`ControlSignal`（Replan/Pause/Resume/Interrupt）经 `RunRegistry.AttachControl` 跨请求投递；失败连击 `replanFailStreak=3` 自动重规划（`EventReplan`） | ✅ 复杂任务可控性 |

---

## 4. 风险与依赖

- **SSH 终端稳定性**：PTY 异步读取需压测（长输出、异常断开、并发会话）；建议补 `terminal_test.go`（真实或 mock SSH server）。
- **指代消解误判**：跨轮实体解析可能错绑，需以显式确认（HITL）兜底，避免危险命令发错机器。
- **RAG 成本**：向量化大仓库的算力/存储，优先做增量索引与文件级分块。
- **文档一致性**：任何架构改动须同步 `design.md` / `README.md` / 本路线图，避免再次出现"多租户 / 账号"式过期描述（Phase 1–4 已统一为单 operator 开源 harness）。

---

## 5. 里程碑建议

- **M1（已完成 ✅）**：SSH 交互终端（含 WS 实时终端）+ 意图指代消解 + 连接管理 UI + 文档一致性修复（单 operator 开源 harness）+ 本路线图落地。
- **M2（已完成 ✅）**：P2 全部收口 —— RAG/Qdrant、自动化评测、TUI 离线终端、MCP SDK 评估，达到"接近生产级"基础，向开源 harness 推进。
- **M3（进行中）**：进入 P3 起步。3.1 多模型路由 + 成本优化 ✅、3.2 技能市场 / 自定义 Skill 上传 ✅、3.3 可观测 + operator 级配额 ✅（Prometheus 配额指标 + `checkQuota` 每日 Token 上限）、3.5 计划-执行-反思可视化与可中断重规划 ✅（`Plan.View`/`Visualize`、`EventPlanUpdate`/`EventReplan`、`ControlSignal` 跨请求控制 + 失败自动重规划）。
- **M4（P3 完成）**：规模化与生态能力（技能市场、可观测看板、计划-执行-反思编排）已随 M3 一并收口，P3 全部完成；SSO（3.4）作为可选扩展单列。

## M5（对标 Grok Build 的能力补齐）

| # | 能力 | 状态 | 关键产出 |
|---|------|------|----------|
| 5.1 | 内核级沙箱三档（只读/workspace/strict） | ✅ | `security.Guard.Mode` + `SandboxMode`；配置 `security.sandbox_mode`；strict 阻断网络出口、readonly 拒绝变更类工具；Windows 退化为路径守护 + 降权 |
| 5.2 | 工具集扩容 | ✅ | 新增 `apply_patch`（统一 diff 结构化编辑）、`lint`、`codecov`、`web_search`（可插拔 `WebSearcher`，默认 DuckDuckGo）；`batch`/`glob`/`grep` 已具备 |
| 5.3 | 用量监控面板 | ✅ | `ChatApp.UsageSnapshot` + `/status` 命令增强 + `GET /api/v1/usage`（token/配额/成本/活跃运行） |
| 5.4 | ACP 协议适配层 | ✅ | `trigger/http/acp.go`：`/acp/sessions`、`/prompt`、`/cancel`、`/control`、`/status` 复用现有会话/控制 API |
| 5.5 | Plan 只读探索期状态机 | ✅ | `ControlPlanExplore`/`ControlPlanImplement`：探索期 `Guard` 切只读、实现期恢复可写；`/plan`、`/plan-implement` 命令 + HTTP 端点 |
| 5.6 | Headless / 后台长任务 | ✅ | `ChatApp.RunBackground` + `POST /api/v1/chat/background`：脱离 HTTP 连接存活，配合 3.5 的 Pause/Resume/Control |

## M5.7（上下文管理提升）

| # | 能力 | 状态 | 关键产出 |
|---|------|------|----------|
| 5.7.1 | 异步压缩 + 可配置阈值 | ✅ | `Compressor.CompactThresholdRatio`（默认 0.8）+ `Pressure`/`ShouldPreCompact`；einoorch `loadAndCompress` 达阈值时后台 `backgroundSummarize` 异步 summarize 并落库，主响应走同步快速压缩不再被 LLM 阻塞；配置 `agent.compact_threshold_ratio` |
| 5.7.2 | 长任务跨段记忆固化 | ✅ | `ChatApp.RunBackground` 事件循环中，每收到 `EventCompress`/`EventDone` 把当前会话滚动摘要 `summaryRepo.Get` → `memSvc.Save`（scope=project, category=task_progress）；新段启动时经 `memory.FormatForPrompt` 自动召回，避免反复压缩丢信息 |
| 5.7.3 | PlanMode 上下文隔离 | ✅ | `engine.Loop` 新增 `planExplore` 标志：`ControlPlanExplore` 时置 true 并清空加载历史（仅系统提示），`ControlPlanImplement` 时置 false 恢复全量加载；与 5.5 的 Guard 只读/可写联动，探索期不累积实现上下文 |
| 5.7.4 | 子代理窗口隔离（可选） | ✅ | `subagent.Runner.SummarizeResult` 回调（loop `SetSubRunner` 注入 `Compressor.Summarizer`）；`Result.Summary` + `FormatResults` 优先回写摘要、标注原文已省略；长任务下子代理结果不再膨胀主窗口 |

**上下文管理**：Grok Build 采用代理侧 + 用户侧双压缩（agent-side compaction 收紧历史、user-side 按需召回）。我们已有 `compress`（双级 summarize）与 `memory` 向量召回；M5.7 补齐了①异步压缩阈值、②长任务跨段记忆固化、③PlanMode 上下文隔离、④子代理窗口隔离。

---

> 历史多租户规划见 [`docs/saas-roadmap.md`](docs/saas-roadmap.md)（已归档，不适用）。
