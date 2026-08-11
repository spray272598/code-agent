package mcp

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/mcp/model"
)

func TestManager_CallTool_NoServer(t *testing.T) {
	m := NewManager()
	defer m.Close()
	_, err := m.CallTool(context.Background(), "nonexistent__tool", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestManager_Health_Empty(t *testing.T) {
	m := NewManager()
	defer m.Close()
	health := m.Health(context.Background())
	// Health 应该返回空列表或 nil
	_ = health // 不 panic 即可
	if len(health) != 0 {
		t.Fatalf("expected 0 health entries, got %d", len(health))
	}
}

func TestManager_ListTools_Empty(t *testing.T) {
	m := NewManager()
	defer m.Close()
	tools, err := m.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestManager_Remove_NonExistent(t *testing.T) {
	m := NewManager()
	defer m.Close()
	// Remove 不存在的服务器应该不报错或返回特定错误
	err := m.Remove("nonexistent")
	_ = err // 不 panic 即可
}

func TestManager_IsOnline_Empty(t *testing.T) {
	m := NewManager()
	defer m.Close()
	if m.IsOnline("nonexistent") {
		t.Fatal("expected false for nonexistent server")
	}
}

func TestManager_ListServers_Empty(t *testing.T) {
	m := NewManager()
	defer m.Close()
	servers := m.ListServers()
	if len(servers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(servers))
	}
}

func TestManager_AddOrUpdate_EmptyName(t *testing.T) {
	m := NewManager()
	defer m.Close()
	// 空名称应返回错误，不应启动任何客户端
	cfg := model.ServerConfig{Name: "", Transport: "stdio", Enabled: true}
	if err := m.AddOrUpdate(context.Background(), cfg); err == nil {
		t.Fatal("expected error for empty server name")
	}
}
