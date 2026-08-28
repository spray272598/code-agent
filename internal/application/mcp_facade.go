package application

import (
	"context"
	"fmt"

	mcpport "github.com/spray272598/code-agent/internal/domain/mcp/adapter/port"
)

type MCPFacade struct {
	factory mcpport.IUserMCPManagerFactory
}

func (m *MCPFacade) MCPFor(ctx context.Context) (mcpport.IMCPManagerPort, error) {
	if m.factory == nil {
		return nil, fmt.Errorf("mcp factory not configured")
	}
	return m.factory.For(ctx)
}
