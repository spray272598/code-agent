# M2 / 2.4b — MCP SDK 评估报告（mark3labs/mcp-go）

> 触发条件（roadmap）：在 `IMCPClient` 接口下评估 `mark3labs/mcp-go` 等 SDK，
> 实际替换的触发信号为「接入 >20 个 server」或「需要 resources / prompts / sampling」。
> 本报告为评估结论，不替换现有手搓实现。

## 1. 评估方法

在 `internal/infrastructure/mcp/eval/` 新建一个可行性 spike：

- `mcpgo_adapter.go`：用 `mark3labs/mcp-go@v0.32.0` 的 `client.Client` 适配现有
  `domain/mcp/adapter/port.IMCPClient`（Name / Initialize / Ping / ListTools /
  CallTool / Close），并额外暴露 `ListResources` / `ListPrompts` 以验证 SDK 对
  resources/prompts 的支持。
- `eval_test.go`：用 mcp-go 自带的 **in-process transport**（内存 server，无需
  真实子进程）注册一个 tool + 一个 resource + 一个 prompt，跑端到端往返，断言
  adapter 同时满足生产端口与 resources/prompts 能力。

`go test ./internal/infrastructure/mcp/eval/` 已全绿，证明 mcp-go 可作为
`IMCPClient` 的合格后端。

## 2. 结论

| 维度 | 现有手搓实现（cmd-based） | mark3labs/mcp-go |
| --- | --- | --- |
| 满足 `IMCPClient` | ✅ | ✅（已验证） |
| stdio / HTTP(SSE) 传输 | ✅ 自研 | ✅ 原生支持，含 OAuth |
| 分页（ListTools 多页） | 需自行处理 | ✅ `ListTools` 内置翻页 |
| resources | ❌ 端口未暴露 | ✅ `ListResources` / `ReadResource` |
| prompts | ❌ 端口未暴露 | ✅ `ListPrompts` / `GetPrompt` |
| sampling（server→client 反向请求） | ❌ | ✅ SDK 提供客户端能力 |
| 依赖体积 | 0 额外依赖 | + cast / uritemplate 等传递依赖 |
| 协议演进维护成本 | 自行跟进 | 社区跟进，省心 |

## 3. 决策

- **当前（M2 阶段）不建议替换**。项目当前 server 数量少、仅用 tools/call，
  手搓实现零额外依赖、行为可控，且已带看门狗/熔断。
- **触发替换的信号**（满足任一即启动 2.4 正式迁移）：
  1. 接入 MCP server 数 > 20，分页/并发/传输多样性成为负担；
  2. 业务需要 resources（向模型喂上下文文件）或 prompts（复用提示模板）；
  3. 需要对接带 OAuth 的远程 MCP server。
- 一旦触发，迁移路径明确：`internal/infrastructure/mcp/eval/mcpgo_adapter.go`
  已证明是 `IMCPClient` 的合法实现，只需把 `Manager` 的客户端构造从 cmd-based
  切换为 `NewStdio`/`NewSSE`/`NewStreamableHTTP`，并把 `IMCPClient` 端口扩展
  `ListResources`/`ListPrompts` 即可。

## 4. 备注

- spike 仅作评估，未接进 `Manager` 生产路径（避免引入未使用的依赖到运行时）。
- `go get github.com/mark3labs/mcp-go@v0.32.0` 已写入 `go.mod`/`go.sum`，
  但其仅被 `internal/infrastructure/mcp/eval` 这一非生产包引用。
