package ssh

import (
	"context"
	"testing"
	"time"
)

func TestExecutor_Exec_NoConnection(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	e := NewExecutor(p)
	_, err := e.Exec(context.Background(), "nonexistent", "ls", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for nonexistent connection")
	}
}

func TestExecutor_ExecStreaming_NoConnection(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	e := NewExecutor(p)
	_, err := e.ExecStreaming(context.Background(), "nonexistent", "ls", 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent connection")
	}
}
