package ssh

import (
	"testing"
)

func TestPool_GetConnection_NotFound(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	_, err := p.GetConnection("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent connection")
	}
}

func TestPool_IsConnected_NotFound(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	if p.IsConnected("nonexistent") {
		t.Fatal("expected false for nonexistent connection")
	}
}

func TestPool_ListConnections_Empty(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	names := p.ListConnections()
	if len(names) != 0 {
		t.Fatalf("expected 0 connections, got %d", len(names))
	}
}

func TestPool_GetConfig_NotFound(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	_, ok := p.GetConfig("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent config")
	}
}

func TestPool_Health_Empty(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	health := p.Health()
	if len(health) != 0 {
		t.Fatalf("expected 0 health entries, got %d", len(health))
	}
}

func TestPool_Disconnect_NotFound(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	err := p.Disconnect("nonexistent")
	if err == nil {
		t.Fatal("expected error for disconnecting nonexistent connection")
	}
}
