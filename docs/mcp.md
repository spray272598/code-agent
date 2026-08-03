# MCP Integration

## Overview

Code-Agent embeds an **MCP manager** (stdio transport) and bridges tools into the agent registry as `server__tool` names when collisions exist.

```
Agent Loop → tool.MapRegistry → MCPTool → Manager.CallTool → StdioClient JSON-RPC
```

All MCP tools pass **security.Guard** (default CONFIRM; deny patterns on args; path sandbox when `path` present).

## Install API

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
