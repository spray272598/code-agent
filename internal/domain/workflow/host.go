package workflow

import "context"

type HostRequest struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

type HostResponse struct {
	Result map[string]any `json:"result,omitempty"`
	Paused bool           `json:"paused,omitempty"`
	Error  error          `json:"-"`
}

type Host interface {
	Execute(ctx context.Context, req HostRequest) (HostResponse, error)
}
