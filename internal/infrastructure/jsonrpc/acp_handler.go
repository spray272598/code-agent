package jsonrpc

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
)

type ACPApp interface {
	RunBackground(ctx context.Context, req ACPChatRequest, onEvent func(map[string]any)) (string, error)
	CancelSession(sessionID, reason string) (bool, error)
	SendControl(sessionID string, signal int, goal string) bool
	UsageSnapshot(ctx context.Context, userID, sessionID string) any
}

type ACPChatRequest struct {
	UserID      string
	SessionID   string
	Message     string
	AutoApprove bool
}

type ACPHandler struct {
	app ACPApp
}

func NewACPHandler(app ACPApp) *ACPHandler {
	return &ACPHandler{app: app}
}

func (h *ACPHandler) RegisterHandlers(server *Server) {
	server.Handle("initialize", h.handleInitialize)
	server.Handle("session/new", h.handleSessionNew)
	server.Handle("session/prompt", h.handleSessionPrompt)
	server.Handle("session/cancel", h.handleSessionCancel)
	server.Handle("session/control", h.handleSessionControl)
	server.Handle("session/status", h.handleSessionStatus)
}

type acpInitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      acpServerInfo  `json:"serverInfo"`
}

type acpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (h *ACPHandler) handleInitialize(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	log.Printf("[acp] initialize")
	return acpInitializeResult{
		ProtocolVersion: "1.0.0",
		Capabilities: map[string]any{
			"sessions": map[string]any{
				"create": true,
				"prompt": true,
				"cancel": true,
			},
		},
		ServerInfo: acpServerInfo{
			Name:    "code-agent",
			Version: "1.0.0",
		},
	}, nil
}

func (h *ACPHandler) handleSessionNew(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	var p struct {
		UserID string `json:"userId"`
		Cwd    string `json:"cwd"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params: "+err.Error())
	}
	if p.UserID == "" {
		p.UserID = "acp-default"
	}
	sid := "acp-" + randHex(8)
	log.Printf("[acp] session/new: id=%s user=%s", sid, p.UserID)
	return map[string]any{"sessionId": sid}, nil
}

func (h *ACPHandler) handleSessionPrompt(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    string `json:"prompt"`
		UserID    string `json:"userId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params: "+err.Error())
	}
	if p.Prompt == "" {
		return nil, NewError(CodeInvalidParams, "prompt required")
	}
	if p.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "sessionId required")
	}
	if p.UserID == "" {
		p.UserID = "acp-default"
	}

	log.Printf("[acp] session/prompt: id=%s", p.SessionID)
	_, err := h.app.RunBackground(ctx, ACPChatRequest{
		UserID:      p.UserID,
		SessionID:   p.SessionID,
		Message:     p.Prompt,
		AutoApprove: true,
	}, nil)
	if err != nil {
		return nil, NewError(CodeInternalError, err.Error())
	}
	return map[string]any{"sessionId": p.SessionID, "accepted": true}, nil
}

func (h *ACPHandler) handleSessionCancel(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params: "+err.Error())
	}
	if p.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "sessionId required")
	}

	log.Printf("[acp] session/cancel: id=%s", p.SessionID)
	ok, _ := h.app.CancelSession(p.SessionID, "acp cancel")
	return map[string]any{"cancelled": ok}, nil
}

func (h *ACPHandler) handleSessionControl(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Signal    string `json:"signal"`
		Goal      string `json:"goal"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params: "+err.Error())
	}
	if p.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "sessionId required")
	}

	sig := acpSignalCode(p.Signal)
	log.Printf("[acp] session/control: id=%s signal=%s", p.SessionID, p.Signal)
	ok := h.app.SendControl(p.SessionID, sig, p.Goal)
	return map[string]any{"delivered": ok}, nil
}

func (h *ACPHandler) handleSessionStatus(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		UserID    string `json:"userId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, NewError(CodeInvalidParams, "invalid params: "+err.Error())
	}
	if p.SessionID == "" {
		return nil, NewError(CodeInvalidParams, "sessionId required")
	}
	if p.UserID == "" {
		p.UserID = "default"
	}

	u := h.app.UsageSnapshot(ctx, p.UserID, p.SessionID)
	return map[string]any{"sessionId": p.SessionID, "usage": u}, nil
}

func acpSignalCode(name string) int {
	switch name {
	case "replan":
		return 1
	case "replan_goal", "replanGoal":
		return 2
	case "pause":
		return 3
	case "resume":
		return 4
	case "interrupt", "cancel":
		return 5
	case "plan_explore", "explore":
		return 6
	case "plan_implement", "implement":
		return 7
	default:
		return 0
	}
}

func randHex(n int) string {
	const chars = "abcdef0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
