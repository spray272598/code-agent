package sshtool

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/tool"
)

func TestExecTool_Name(t *testing.T) {
	et := NewExecTool(nil)
	if et.Name() != "ssh_exec" {
		t.Fatalf("expected ssh_exec, got %s", et.Name())
	}
}

func TestExecTool_Description(t *testing.T) {
	et := NewExecTool(nil)
	if et.Description() == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestExecTool_InputSchema(t *testing.T) {
	et := NewExecTool(nil)
	schema := et.InputSchema()
	if schema["type"] != "object" {
		t.Fatal("expected object type")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["connection"]; !ok {
		t.Fatal("expected connection property")
	}
	if _, ok := props["command"]; !ok {
		t.Fatal("expected command property")
	}
	if _, ok := props["timeout_ms"]; !ok {
		t.Fatal("expected timeout_ms property")
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("expected required slice")
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required fields, got %d", len(required))
	}
}

func TestExecTool_Execute_MissingArgs(t *testing.T) {
	et := NewExecTool(nil)
	res, err := et.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing args")
	}
}

func TestExecTool_Execute_MissingCommand(t *testing.T) {
	et := NewExecTool(nil)
	res, err := et.Execute(context.Background(), map[string]any{"connection": "srv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing command")
	}
}

func TestReadFileTool_Name(t *testing.T) {
	ft := NewReadFileTool(nil)
	if ft.Name() != "ssh_read_file" {
		t.Fatalf("expected ssh_read_file, got %s", ft.Name())
	}
}

func TestReadFileTool_InputSchema(t *testing.T) {
	ft := NewReadFileTool(nil)
	schema := ft.InputSchema()
	if schema["type"] != "object" {
		t.Fatal("expected object type")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["connection"]; !ok {
		t.Fatal("expected connection property")
	}
	if _, ok := props["path"]; !ok {
		t.Fatal("expected path property")
	}
}

func TestReadFileTool_Execute_MissingArgs(t *testing.T) {
	ft := NewReadFileTool(nil)
	res, _ := ft.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing args")
	}
}

func TestWriteFileTool_Name(t *testing.T) {
	ft := NewWriteFileTool(nil)
	if ft.Name() != "ssh_write_file" {
		t.Fatalf("expected ssh_write_file, got %s", ft.Name())
	}
}

func TestWriteFileTool_InputSchema(t *testing.T) {
	ft := NewWriteFileTool(nil)
	schema := ft.InputSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["content"]; !ok {
		t.Fatal("expected content property")
	}
}

func TestWriteFileTool_Execute_MissingArgs(t *testing.T) {
	ft := NewWriteFileTool(nil)
	res, _ := ft.Execute(context.Background(), map[string]any{"connection": "srv"})
	if !res.IsError {
		t.Fatal("expected error for missing path")
	}
}

func TestListDirTool_Name(t *testing.T) {
	ft := NewListDirTool(nil)
	if ft.Name() != "ssh_list_dir" {
		t.Fatalf("expected ssh_list_dir, got %s", ft.Name())
	}
}

func TestListDirTool_InputSchema(t *testing.T) {
	ft := NewListDirTool(nil)
	schema := ft.InputSchema()
	if schema["type"] != "object" {
		t.Fatal("expected object type")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["path"]; !ok {
		t.Fatal("expected path property")
	}
}

func TestListDirTool_Execute_MissingArgs(t *testing.T) {
	ft := NewListDirTool(nil)
	res, _ := ft.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing args")
	}
}
