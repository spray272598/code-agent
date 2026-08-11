package ssh

import (
	"context"
	"testing"
)

func TestFileTransfer_ReadFile_NoConnection(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	ft := NewFileTransfer(p)
	_, err := ft.ReadFile(context.Background(), "nonexistent", "/tmp/test")
	if err == nil {
		t.Fatal("expected error for nonexistent connection")
	}
}

func TestFileTransfer_WriteFile_NoConnection(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	ft := NewFileTransfer(p)
	err := ft.WriteFile(context.Background(), "nonexistent", "/tmp/test", []byte("test"))
	if err == nil {
		t.Fatal("expected error for nonexistent connection")
	}
}

func TestFileTransfer_ListDir_NoConnection(t *testing.T) {
	p := NewPool()
	defer p.CloseAll()
	ft := NewFileTransfer(p)
	_, err := ft.ListDir(context.Background(), "nonexistent", "/tmp")
	if err == nil {
		t.Fatal("expected error for nonexistent connection")
	}
}
