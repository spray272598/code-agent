# 本机一键演示（Host Agent + Eval 报告）

## 目标

在**本机仓库**上跑 Coding Agent（类 Claude Code）：Server 管控 + host-agent 执行工具。

## 一键启动

```powershell
cd D:\project_go\code-agent

# mock LLM + host（本地零 Key）
powershell -File scripts/dev_local.ps1

# 指定工作区
powershell -File scripts/dev_local.ps1 -Workspace D:\your\repo

# 真实大模型（需 LLM_API_KEY）
$env:LLM_API_KEY="sk-..."
$env:LLM_API_BASE="https://api.siliconflow.cn/v1"
powershell -File scripts/dev_local.ps1 -RealLLM -Workspace .
```

脚本会：

1. `go build` server / host-agent / cli  
2. 用 `configs/config.host.yaml`（`prefer_host: true`）起 Server  
3. 起 host-agent 连 `ws://127.0.0.1:8080/ws/host`  
4. 打印 CLI / Eval 命令  

另开终端：

```powershell
.\bin\cli.exe --base http://127.0.0.1:8080 --key dev-key
# 或
powershell -File scripts/eval_report.ps1
```

## 验证 host 在线

```powershell
curl -H "X-API-Key: dev-key" http://127.0.0.1:8080/api/v1/host/devices
```

`online >= 1` 时，coding 工具优先在 host 工作区执行；离线则回落 server `workspace_root`。

## Eval 数字报告

```powershell
# server 已启动
powershell -File scripts/eval_report.ps1
# 产物
#   reports/eval-latest.json
#   reports/eval-latest.md
```

指标：

- `pass` / `total` / `pass_rate`
- 每 case 延迟 `latency_ms`
- 默认阈值 `MinPassRate=0.8`（可用参数改）

```powershell
powershell -File scripts/eval_report.ps1 -MinPassRate 0.9 -OutFile reports/eval-$(Get-Date -Format yyyyMMdd).json
```

## 与编排关系

| 模式 | 说明 |
|------|------|
| mock / 无 Key | `native-offline` + host 执行（仍有完整 Guard） |
| RealLLM | `eino` 编排 + host 执行 + GuardedTool |

## 手动双终端（不用脚本）

```powershell
# T1
go run ./cmd/server -config configs/config.host.yaml

# T2
go run ./cmd/host-agent --server ws://127.0.0.1:8080/ws/host --token dev-key --workspace . --reconnect

# T3
go run ./cmd/cli --key dev-key
```
