# Docker 部署指南

## 快速开始

```bash
# 1. 复制环境变量模板（修改里面的配置）
cp .env.example .env

# 2. 仅启动中间件（MySQL、Redis、MinIO、Jaeger）
docker compose up -d

# 3. 全栈启动（含 code-agent server，distroless 生产镜像）
docker compose --profile app up -d --build

# 4. 调试模式启动（debian-slim 镜像，带 shell/curl/wget）
docker compose --profile dev up -d --build
```

## 服务地址

| 服务 | 地址 | 说明 |
|------|------|------|
| **Code-Agent Server** | http://localhost:8080 | AI Agent API |
| **MySQL** | localhost:3306 | root / MYSQL_ROOT_PASSWORD / code_agent |
| **Redis** | localhost:6379 | 默认无密码 |
| **MinIO API** | http://localhost:9000 | S3 兼容存储 |
| **MinIO Console** | http://localhost:9001 | 对象存储管理界面 |
| **Jaeger UI** | http://localhost:16686 | 分布式追踪界面 |
| **OTLP HTTP** | localhost:4318 | 指标/trace 上报端点 |

## 健康检查

```bash
# Server 健康检查（所有模式通用）
curl -H "X-API-Key: dev-key" http://localhost:8080/health

# 查看服务状态
docker compose ps

# 查看所有服务日志
docker compose logs -f

# 查看单个服务日志
docker compose logs -f server
```

## 配置说明

### 环境变量 (.env)

复制 `.env.example` 为 `.env` 并按需修改：

```bash
# 真模型配置
LLM_USE_MOCK=false
LLM_API_KEY=sk-your-key
LLM_API_BASE=https://api.siliconflow.cn/v1
LLM_MODEL=deepseek-ai/DeepSeek-V3

# API Key（生产环境务必修改）
CODE_AGENT_API_KEY=your-secret-key
```

### 配置文件

- **`configs/config.docker.yaml`** — 容器内运行时配置（通过 volume 挂载）
- **`configs/config.example.yaml`** — 开发参考配置

### Docker Compose Profiles

| Profile | 说明 | 镜像 |
|---------|------|------|
| `(none)` | 仅中间件 | — |
| `app` | 全栈（生产） | distroless (最小攻击面) |
| `dev` | 全栈（调试） | debian-slim (含 shell) |

## Dockerfile 构建目标

```bash
# 生产镜像（默认）
docker build -t code-agent:latest .

# 开发/调试镜像（含 bash、wget、curl）
docker build --target server-dev -t code-agent:dev .

# CLI 工具镜像
docker build --target cli -t code-agent-cli:latest .

# Host Agent 镜像
docker build --target host-agent -t code-agent-host:latest .

# 带版本号构建
docker build --build-arg VERSION=v1.0.0 -t code-agent:v1.0.0 .
```

## 数据持久化

所有数据通过 Docker Named Volumes 持久化：

| Volume | 用途 |
|--------|------|
| `mysql_data` | MySQL 数据库文件 |
| `redis_data` | Redis 数据 |
| `minio_data` | MinIO 对象存储 |
| `jaeger_data` | Jaeger 追踪数据 |
| `app_data` | Code-Agent 运行时数据（checkpoints、对象存储降级） |

**本地 workspace** 通过 bind mount 挂载：`./workspace:/data/workspace`

## 真模型部署

```bash
# 1. 修改 .env，设置真实 LLM 配置
#    LLM_USE_MOCK=false
#    LLM_API_KEY=sk-...

# 2. 重启全栈
docker compose --profile app down
docker compose --profile app up -d --build

# 3. 验证
curl -H "X-API-Key: your-key" http://localhost:8080/health
```

## 端口冲突处理

如果默认端口被占用，修改 `.env` 中的变量：

```bash
SERVER_PORT=8081    # Server
# MySQL/Redis/MinIO/Jaeger 端口在 docker-compose.yml 中修改
```

## 常见问题

### Q: 首次启动 MySQL 很慢？
A: MySQL 8.0 首次初始化需要 10-30 秒，`docker compose ps` 会显示 `healthy` 后才能被 server 连接。

### Q: MinIO bucket 自动创建失败？
A: 检查 `minio-init` 日志：`docker compose logs minio-init`。确认 MinIO 健康后才会执行。

### Q: Server 连接 MySQL 失败？
A: 确认 MySQL 已健康：`docker compose exec mysql mysqladmin ping -h 127.0.0.1 -u root -p123456`。

### Q: 如何进入容器调试？
A: 使用 dev 镜像：`docker compose --profile dev up -d --build`，然后 `docker exec -it code-agent-server-dev bash`。

### Q: distroless 镜像如何检查健康？
A: distroless 无 shell，从宿主机检查：`curl -H "X-API-Key: dev-key" http://localhost:8080/health`。

### Q: 清理所有数据？
A: ⚠️ **危险操作**：`docker compose down -v` 会删除所有 Named Volumes。

## 安全提示

1. **生产环境务必修改所有默认密码**（`CODE_AGENT_API_KEY`、`MYSQL_ROOT_PASSWORD`、`MINIO_ROOT_PASSWORD`）
2. distroless 镜像无 shell，即使被攻破也无法进入容器
3. 所有数据卷都设置了正确的持久化
4. 网络隔离：所有服务在 `code-agent-net` 私有网络内通信