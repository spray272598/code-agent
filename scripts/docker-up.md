# 本机 Docker 中间件

```bash
cd D:\project_go\code-agent
docker compose up -d
```

| 服务 | 地址 |
|------|------|
| MySQL | localhost:3306 / root / 123456 / code_agent |
| Redis | localhost:6379 |
| MinIO API | http://localhost:9000 (minioadmin/minioadmin) |
| MinIO Console | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 |
| OTLP HTTP | localhost:4318 |

## 启用 MinIO + OTLP 跑 Server

```powershell
# configs/config.yaml 可改，或环境变量：
$env:OTLP_ENABLED="true"
$env:OTLP_ENDPOINT="localhost:4318"
# storage.enabled=true, endpoint http://127.0.0.1:9000

go run ./cmd/server -config configs/config.yaml
```

## Host Agent（本机改代码）

一键：

```powershell
powershell -File scripts/dev_local.ps1 -Workspace D:\your\repo
```

手动：

```powershell
# 终端1
go run ./cmd/server -config configs/config.host.yaml
# 终端2
go run ./cmd/host-agent --server ws://127.0.0.1:8080/ws/host --token dev-key --workspace D:\your\repo --reconnect
# 查看在线
curl -H "X-API-Key: dev-key" http://127.0.0.1:8080/api/v1/host/devices
# 评测数字
powershell -File scripts/eval_report.ps1
```
