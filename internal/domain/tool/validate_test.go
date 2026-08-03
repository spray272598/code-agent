package tool

import "testing"

func TestValidateArgsRequired(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
	if err := ValidateArgs(schema, map[string]any{}); err == nil {
		t.Fatal("want missing path")
	}
	if err := ValidateArgs(schema, map[string]any{"path": "a.go"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateArgsType(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"regex": map[string]any{"type": "boolean"},
		},
	}
	if err := ValidateArgs(schema, map[string]any{"regex": "yes"}); err == nil {
		t.Fatal("want type error")
	}
	if err := ValidateArgs(schema, map[string]any{"regex": true}); err != nil {
		t.Fatal(err)
	}
}

func TestIsReadOnly(t *testing.T) {
	if !IsReadOnly("read_file") || !IsReadOnly("demo__grep") {
		t.Fatal("expected read-only")
	}
	if IsReadOnly("write_file") || IsReadOnly("bash") {
		t.Fatal("expected write")
	}
}
