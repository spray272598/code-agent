# MCP Integration

## Overview

Code-Agent embeds an **MCP manager** (stdio + HTTP transports) and bridges tools into the agent registry as `server__tool` names when collisions exist.

```
Agent Loop → tool.MapRegistry → MCPTool → Manager.CallTool → StdioClient/HTTPClient JSON-RPC
```

All MCP tools pass **security.Guard** (default CONFIRM; deny patterns on args; path sandbox when `path` present).

## Install methods

### 1. mcp.json file (recommended, VS Code / Claude Desktop style)

Create `mcp.json` and point to it from config:

```yaml
# configs/config.yaml
mcp:
  enabled: true
  config_file: "./mcp.json"
```

```json
{
  "mcpServers": {
    "fetch": {
      "type": "stdio",
      "command": "uvx",
      "args": ["mcp-server-fetch"]
    },
    "remote": {
      "type": "http",
      "url": "https://example.com/mcp"
    }
  }
}
```

Servers are loaded automatically at startup. See `mcp.json.example`.

### 2. Install API (runtime)

```http
POST /api/v1/mcp/servers
X-API-Key: <key>
Content-Type: application/json

{
  "name": "demo",
  "transport": "stdio",
  "command": "./bin/mcp-demo.exe",
  "args": [],
  "enabled": true,
  "timeoutSec": 30
}
```

## mcp.json field mapping

| mcp.json key | Domain `ServerConfig` | Notes |
|---|---|---|
| `type` | `Transport` | `stdio` (default) \| `sse` \| `http` |
| `command` | `Command` | required for stdio |
| `args` | `Args` | e.g. `["mcp-server-fetch", "--ignore-robots-txt"]` |
| `env` | `Env` | process env for stdio |
| `headers` | `Env` | extra HTTP headers (merged into Env) |
| `url` | `URL` | required for sse/http |
| `enabled` | `Enabled` | default `true` |
| `timeout` | `TimeoutSec` | seconds, default 60 |

## Health / list

- `GET /api/v1/mcp/health`
- `GET /api/v1/mcp/tools`
- `DELETE /api/v1/mcp/servers?name=demo`

## Reconnect

- Watchdog every 15s restarts offline enabled servers
- `CallTool` failure (EOF / broken pipe) triggers reconnect + one retry

## Windows notes

- Child process uses `CREATE_NO_WINDOW` / `HideWindow` to avoid console flash
- Prefer absolute path to `.exe` in `command`

## Demo binary

```bash
go build -o bin/mcp-demo.exe ./cmd/mcp-demo
# server auto-loads if ./mcp-demo.exe or ./bin/mcp-demo.exe exists
```

## Security tips

- Do not auto-approve MCP write/exec tools
- Scope MCP command binary under trusted install paths
- Treat MCP results as untrusted input in prompts
