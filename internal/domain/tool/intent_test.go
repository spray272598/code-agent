package tool

import (
	"testing"
)

func TestDefaultPresenter(t *testing.T) {
	p := DefaultPresenter{}
	call := p.PresentCall(map[string]any{"command": "ls"})
	if call.Type != IntentTerminal {
		t.Errorf("expected terminal, got %s", call.Type)
	}
	result := p.PresentResult(Result{Text: "ok"})
	if result.Type != IntentTerminal {
		t.Errorf("expected terminal, got %s", result.Type)
	}
}

func TestDiffPresenterEdit(t *testing.T) {
	p := DiffPresenter{}
	call := p.PresentCall(map[string]any{
		"path":       "main.go",
		"old_string": "func foo()",
		"new_string": "func bar()",
	})
	if call.Type != IntentDiff {
		t.Errorf("expected diff, got %s", call.Type)
	}
	if call.Title != "Edit main.go" {
		t.Errorf("unexpected title: %s", call.Title)
	}
}

func TestDiffPresenterWrite(t *testing.T) {
	p := DiffPresenter{}
	call := p.PresentCall(map[string]any{
		"path":    "new.go",
		"content": "package main",
	})
	if call.Type != IntentDiff {
		t.Errorf("expected diff, got %s", call.Type)
	}
	if call.Summary != "12 bytes" {
		t.Errorf("unexpected summary: %s", call.Summary)
	}
}

func TestLocationsPresenter(t *testing.T) {
	p := LocationsPresenter{}
	call := p.PresentCall(map[string]any{
		"pattern": "TODO",
		"path":    "src/",
	})
	if call.Type != IntentLocations {
		t.Errorf("expected locations, got %s", call.Type)
	}
	if call.Title != "Search TODO in src/" {
		t.Errorf("unexpected title: %s", call.Title)
	}

	result := p.PresentResult(Result{Text: "src/a.go:5:TODO item\nsrc/b.go:10:TODO fix\n"})
	if result.Summary != "2 locations" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestGetPresenter(t *testing.T) {
	tests := []struct {
		tool string
		want IntentType
	}{
		{"edit_file", IntentDiff},
		{"apply_patch", IntentDiff},
		{"write_file", IntentDiff},
		{"grep", IntentLocations},
		{"glob", IntentLocations},
		{"bash", IntentTerminal},
		{"read_file", IntentTerminal},
	}
	for _, tt := range tests {
		p := GetPresenter(tt.tool)
		call := p.PresentCall(nil)
		if call.Type != tt.want {
			t.Errorf("GetPresenter(%q): expected %s, got %s", tt.tool, tt.want, call.Type)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short: got %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("long: got %q", got)
	}
}
