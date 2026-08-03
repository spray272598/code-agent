package dto

import "github.com/spray272598/code-agent/internal/application"

// ToAppChat maps edge DTO → application request (mapping layer).
func ToAppChat(r ChatRequest) application.ChatRequest {
	return application.ChatRequest{
		SessionID: r.SessionID, UserID: r.UserID, ProjectID: r.ProjectID,
		Message: r.Message, AutoApprove: r.AutoApprove,
	}
}

// FromAppChat maps application response → edge DTO.
func FromAppChat(r *application.ChatResponse) *ChatResponse {
	if r == nil {
		return nil
	}
	return &ChatResponse{
		SessionID: r.SessionID, Response: r.Response, Steps: r.Steps,
		ToolCalls: r.ToolCalls, TokenUsed: r.TokenUsed,
		NeedPermission: r.NeedPermission, Pending: r.Pending,
		ErrorClass: r.ErrorClass, Slash: r.Slash,
	}
}
