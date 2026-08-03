package dto

// APIResponse is the unified envelope for all JSON APIs.
// code "0000" = success; HTTP status mirrors severity (400/401/404/500).
type APIResponse struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// OK builds a success envelope.
func OK(data any) APIResponse {
	return APIResponse{Code: "0000", Data: data}
}

// Fail builds an error envelope.
func Fail(code, message string) APIResponse {
	if code == "" {
		code = "400"
	}
	return APIResponse{Code: code, Message: message}
}

// PermissionApproveRequest POST /api/v1/permission/approve
type PermissionApproveRequest struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"` // once|session|always
	Continue  bool   `json:"continue"`
	SessionID string `json:"sessionId"`
	UserID    string `json:"userId"`
	// InlineMessage optional user note after approve (defaults to 继续)
	InlineMessage string `json:"inlineMessage,omitempty"`
}

// LogLevelRequest POST /api/v1/admin/log-level
type LogLevelRequest struct {
	Level string `json:"level"`
}
