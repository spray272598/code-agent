# Code-Agent 路线图与后续工作规划（toC）

> **方向**：面向个人开发者（toC），**无企业/组织/多租户概念**；账号 = 邮箱 + 密码 + JWT；所有资源按 `user_id` 隔离。
> **目的**：对齐产品定位、沉淀已落地能力、规划后续迭代；替代已归档的 [`docs/saas-roadmap.md`](docs/saas-roadmap.md)（多租户方案，已不适用）。
> **更新**：2026-08-21

---

## 1. 当前已落地能力（基线）

| 域 | 状态 | 说明 |
|----|------|------|
| **账号（toC）** | ✅ 已落地 | 邮箱+密码注册/登录（bcrypt）、JWT（access+refresh）、邮箱验证、密码重置；`user_id` 隔离；SQLite/MySQL/memory 三后端 |
| **编排** | ✅ 已落地 | Eino ReAct 主路径 + native-offline 兜底；`/team` 并行子代理；SSE 流式 |
| **安全** | ✅ 已落地 | 五层 Guard、路径/命令归一化、HITL、Hook abort、审计、Redis 限流 |
| **本地工具** | ✅ 已落地 | read/write/edit/bash/glob/grep + memory + delegate；进程隔离 |
| **远程 SSH** | ✅ 已落地 | `ssh_exec` / `ssh_read_file` / `ssh_write_file` / `ssh_list_dir` / **`ssh_terminal`（交互式 PTY 终端）**；连接凭据 KMS 加密存储 |
| **SSH 实时终端** | ✅ 已落地 | 后端 `ws.SSHTerminalHub`（WebSocket 代理 PTY），前端 xterm.js 终端页 `/ssh-terminal`；`ITerminal.Read` 异步缓冲（已补单测） |
| **SSH 连接管理 UI** | ✅ 已落地 | 前端 `/ssh-connections`：列表/新增/删除，KMS 加密存储 |
| **意图识别** | ✅ 已落地 | 规则 + LLM 兜底；新增**跨轮指代消解**（`EntityContext` + `ExtractEntities`，解析"那台机器/刚才那个文件"等），已接入 runner |
| **MCP** | ✅ 已落地 | stdio 热装，`server__tool` 注册，与 core 工具同一 GuardedTool 横切 |
| **记忆/压缩** | ✅ 已落地 | Skill / 长期记忆 / L0–L3 压缩 / Token 预算 |
| **前端** | ✅ 已落地 | React/TS SPA（web/）：账号流 + 对话 |
| **可观测/存储** | ✅ 已落地 | OTLP/Prometheus、MinIO、host-agent |

> 与设计稿、README 对齐：[docs/design.md](docs/design.md) · [README.md](../README.md)

---

## 2. 相对专项工具的短板（需提升）

基于与同类项目（如 Java 系 SSH 运维工具）的对比，我们**鉴定并补齐**了以下差距：

1. **交互式终端**：已在 `internal/infrastructure/ssh/terminal.go` 实现异步缓冲读取，新增 `ssh_terminal` 工具，并补齐 `terminal_test.go`；WebSocket 实时终端（`ws.SSHTerminalHub` + 前端 xterm.js）已落地。
   - *待提升*：会话持久化、多会话标签。
2. **意图识别深度**：当前为「规则 + LLM 兜底」路由（continue/deep/team/normal），**缺少跨轮指代消解**（"在那台机器上跑一下"中的"那台"）。
3. **编排成熟度**：Eino 已够用；相比 Google ADK 类编排框架，缺更结构化的"计划-执行-反思"可视化与可中断重规划。
4. **文档时效**：已修订 README / design.md，将"多租户 SaaS"陈旧描述替换为 toC 事实（见本文件与归档说明）。

---

## 3. 后续工作规划（按优先级）

### P1 — 近期（产品可用性与体验闭环）【已完成 ✅】

| # | 任务 | 关联代码 | 状态 |
|---|------|----------|------|
| 1.1 | **WebSocket 实时终端**：`ws.SSHTerminalHub` 代理 PTY；前端 xterm.js 终端页 `/ssh-terminal` | `trigger/ws/ssh_terminal_hub.go`、`web/src/pages/SSHTerminal.tsx` | ✅ |
| 1.2 | **意图指代消解**：`EntityContext` + `ExtractEntities`，解析"那台机器/刚才那个文件" | `domain/intent/classifier.go`、接入 `einoorch/runner.go` | ✅ |
| 1.3 | **Web 账号流完善**：注册/验证/重置密码页（既有），本批修正侧栏过时多租户文案 | `web/src/*` | ✅（基础） |
| 1.4 | **SSH 连接管理 UI**：`/ssh-connections` 列表/新增/删除 | `web/src/pages/SSHConnections.tsx` | ✅ |

### P2 — 中期（能力深化与质量）

| # | 任务 | 关联代码 | 价值 |
|---|------|----------|------|
| 2.1 | **代码 RAG / Qdrant**：仓库向量索引 + 检索增强生成，提升跨文件理解 | 新增 `retriever` + Qdrant 适配 | 大仓库问答质量 |
| 2.2 | **自动化评测体系**：固化 eval 数据集 + 评分脚本（pass_rate/延迟/成本），纳入 CI | `scripts/eval_*`、新增 eval 框架 | 回归防护、量化迭代 |
| 2.3 | **TUI 终端**：本地终端界面（参考已规划 TUI），凭据本地加密、跨设备撤销 | `cmd/`、新增 TUI 包 | 离线/本机场景 |
| 2.4 | **MCP SDK 评估**：在 `IMCPClient` 接口下评估 `mark3labs/mcp-go` 等 SDK（触发：接 >20 个 server 或需 resources/prompts/sampling） | `domain/tool`（MCP） | 降低长尾维护成本 |

### P3 — 远期（规模化与生态）

| # | 任务 | 价值 |
|---|------|------|
| 3.1 | 多模型路由 + 成本优化（按任务选模型） | 降本 |
| 3.2 | 技能市场 / 自定义 Skill 上传 | 生态 |
| 3.3 | 可观测看板（Grafana：QPS/错误率/超时/消耗）、用户级配额与限流 | 运营 |
| 3.4 | OAuth / 第三方登录（GitHub/Google） | 注册转化 |
| 3.5 | 计划-执行-反思可视化与可中断重规划（编排增强） | 复杂任务可控性 |

---

## 4. 风险与依赖

- **SSH 终端稳定性**：PTY 异步读取需压测（长输出、异常断开、并发会话）；建议补 `terminal_test.go`（真实或 mock SSH server）。
- **指代消解误判**：跨轮实体解析可能错绑，需以显式确认（HITL）兜底，避免危险命令发错机器。
- **RAG 成本**：向量化大仓库的算力/存储，优先做增量索引与文件级分块。
- **文档一致性**：任何架构改动须同步 `design.md` / `README.md` / 本路线图，避免再次出现"多租户"式过期描述。

---

## 5. 里程碑建议

- **M1（已完成 ✅）**：SSH 交互终端（含 WS 实时终端）+ 意图指代消解 + 连接管理 UI + 文档一致性修复（toC）+ 本路线图落地。
- **M2（P1 完成，进行中）**：RAG/Qdrant、自动化评测、TUI、MCP SDK 评估，向"可对外试用的 toC 产品"推进。
- **M3（P2 完成）**：RAG + 自动化评测 + TUI，达到"接近生产级"。
- **M4（P3 完成）**：规模化与生态能力。

---

> 历史多租户规划见 [`docs/saas-roadmap.md`](docs/saas-roadmap.md)（已归档，不适用）。
