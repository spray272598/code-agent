package http

// Health check, admin log-level control, and API documentation endpoints
// (OpenAPI JSON + Swagger UI).

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/spray272598/code-agent/internal/observability"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

func (s *Server) handleLogLevel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"level": observability.LogLevel()}})
	case http.MethodPost, http.MethodPut:
		var body struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, errMap(err))
			return
		}
		observability.SetLogLevel(body.Level)
		writeJSON(w, 200, map[string]any{"code": "0000", "data": map[string]any{"level": observability.LogLevel()}})
	default:
		writeJSON(w, 405, map[string]any{"code": "405"})
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(openAPISpec))
}

func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Code-Agent API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"/>
</head><body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
SwaggerUIBundle({url:'/api/v1/openapi.json', dom_id:'#swagger-ui'});
</script>
</body></html>`))
}

// OpenAPI 3.0 aligned with handlers + dto package (envelope code/message/data).
const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {"title": "Code-Agent API", "version": "1.1.0",
    "description": "Coding agent API. Success: {code:0000,data}. Error: {code,message}. Auth: X-API-Key or Bearer."},
  "servers": [{"url": "/"}],
  "components": {
    "securitySchemes": {
      "ApiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
      "BearerAuth": {"type": "http", "scheme": "bearer"}
    },
    "schemas": {
      "Envelope": {"type":"object","properties":{"code":{"type":"string"},"message":{"type":"string"},"data":{}}},
      "ChatRequest": {"type":"object","required":["message"],"properties":{
        "sessionId":{"type":"string"},"userId":{"type":"string"},"projectId":{"type":"string"},
        "message":{"type":"string"},"autoApprove":{"type":"boolean"}}},
      "PermissionApprove": {"type":"object","required":["id"],"properties":{
        "id":{"type":"string"},"scope":{"type":"string","enum":["once","session","always"]},
        "continue":{"type":"boolean","description":"inline resume agent after approve"},
        "sessionId":{"type":"string"},"userId":{"type":"string"},"inlineMessage":{"type":"string"}}}
    }
  },
  "security": [{"ApiKeyAuth": []}, {"BearerAuth": []}],
  "paths": {
    "/health": {"get": {"summary": "Health", "security": [], "responses": {"200": {"description": "ok"}}}},
    "/api/v1/session": {
      "post": {"summary": "Create session", "responses": {"200": {"description": "envelope+sessionId"}}},
      "get": {"summary": "Get session", "parameters": [{"name":"sessionId","in":"query","schema":{"type":"string"}}], "responses": {"200": {"description": "ok"}}}
    },
    "/api/v1/session/list": {"get": {"summary": "List sessions", "parameters": [{"name":"userId","in":"query","schema":{"type":"string"}}], "responses": {"200": {"description": "ok"}}}},
    "/api/v1/chat": {"post": {"summary": "Chat sync", "requestBody": {"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/ChatRequest"}}}},
      "responses": {"200": {"description": "ChatResponse in data"}, "400": {"description": "error envelope"}}}},
    "/api/v1/chat/stream": {"post": {"summary": "Chat SSE (heartbeat : ping)", "requestBody": {"content":{"application/json":{"schema":{"$ref":"#/components/schemas/ChatRequest"}}}},
      "responses": {"200": {"description": "text/event-stream"}}}},
    "/api/v1/tools": {"get": {"summary": "List tools"}},
    "/api/v1/permission/pending": {"get": {"summary": "Pending permissions", "parameters":[{"name":"sessionId","in":"query","schema":{"type":"string"}}]}},
    "/api/v1/permission/approve": {"post": {"summary": "Approve (+ optional inline continue)", "requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/PermissionApprove"}}}}}},
    "/api/v1/permission/reject": {"post": {"summary": "Reject permission"}},
    "/api/v1/mcp/servers": {"get": {"summary": "MCP health list"}, "post": {"summary": "Install/update MCP"}, "delete": {"summary": "Remove MCP"}},
    "/api/v1/mcp/health": {"get": {"summary": "MCP health"}},
    "/api/v1/mcp/tools": {"get": {"summary": "MCP tools"}},
    "/api/v1/skills": {"get": {"summary": "List skills"}},
    "/api/v1/skills/install": {"post": {"summary": "Install skill from path"}},
    "/api/v1/skills/uninstall": {"post": {"summary": "Uninstall skill"}},
    "/api/v1/skills/reload": {"post": {"summary": "Reload skills"}},
    "/api/v1/memory": {"get": {"summary": "List/search memory"}, "post": {"summary": "Save memory"}},
    "/api/v1/metrics": {"get": {"summary": "JSON metrics"}},
    "/api/v1/audit": {"get": {"summary": "Audit log", "parameters":[{"name":"sessionId","in":"query","schema":{"type":"string"}}]}},
    "/api/v1/blobs": {"get": {"summary": "Get blob", "parameters":[{"name":"key","in":"query","required":true,"schema":{"type":"string"}}]}},
    "/api/v1/host/devices": {"get": {"summary": "Host agents online"}},
    "/api/v1/session/cancel": {"post": {"summary": "Cancel active agent run + checkpoint"}},
    "/api/v1/session/checkpoint": {"get": {"summary": "Get session checkpoint", "parameters":[{"name":"sessionId","in":"query","required":true,"schema":{"type":"string"}}]}},
    "/api/v1/session/checkpoints": {"get": {"summary": "List checkpoints", "parameters":[{"name":"status","in":"query","schema":{"type":"string"}}]}},
    "/api/v1/session/runs": {"get": {"summary": "Active in-process runs"}},
    "/api/v1/index/search": {"get": {"summary": "Code index search", "parameters":[{"name":"q","in":"query","required":true,"schema":{"type":"string"}}]}},
    "/api/v1/index/rebuild": {"post": {"summary": "Rebuild code index"}},
    "/api/v1/index/stats": {"get": {"summary": "Code index stats"}},
    "/api/v1/device/code": {"post": {"summary": "RFC8628 device authorization (issue device_code + user_code)", "security": [], "responses": {"200": {"description": "deviceCode,userCode,verificationUri,expiresIn,interval"}}}},
    "/api/v1/device/token": {"post": {"summary": "RFC8628 device token polling (grant_type=device_code)", "security": [], "responses": {"200": {"description": "tokens once approved"}, "400": {"description": "authorization_pending / slow_down / access_denied / expired_token / invalid_grant"}}}},
    "/api/v1/device/approve": {"post": {"summary": "Approve/deny a device by user_code (requires Bearer JWT)", "responses": {"200": {"description": "ok"}}}},
    "/api/v1/admin/log-level": {"get": {"summary": "Get log level"}, "post": {"summary": "Set log level"}},
    "/api/v1/openapi.json": {"get": {"summary": "This document", "security": []}},
    "/metrics": {"get": {"summary": "Prometheus text", "security": []}},
    "/docs": {"get": {"summary": "Swagger UI", "security": []}},
    "/ws/host": {"get": {"summary": "Host-agent WebSocket", "parameters":[
      {"name":"token","in":"query","schema":{"type":"string"}},
      {"name":"deviceId","in":"query","schema":{"type":"string"}}]}}
  }
}`
