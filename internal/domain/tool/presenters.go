package tool

import (
	"fmt"
	"strings"
)

// DiffPresenter renders edit/patch operations as diffs.
type DiffPresenter struct{}

func (d DiffPresenter) PresentCall(args map[string]any) RenderIntent {
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if oldStr != "" && newStr != "" {
		return RenderIntent{
			Type:    IntentDiff,
			Title:   fmt.Sprintf("Edit %s", path),
			Summary: fmt.Sprintf("replace %d → %d chars", len(oldStr), len(newStr)),
		}
	}
	content, _ := args["content"].(string)
	return RenderIntent{
		Type:    IntentDiff,
		Title:   fmt.Sprintf("Write %s", path),
		Summary: fmt.Sprintf("%d bytes", len(content)),
	}
}

func (d DiffPresenter) PresentResult(result Result) RenderIntent {
	return RenderIntent{Type: IntentDiff, Summary: truncate(result.Text, 100)}
}

// LocationsPresenter renders grep/glob results as file locations.
type LocationsPresenter struct{}

func (l LocationsPresenter) PresentCall(args map[string]any) RenderIntent {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	title := fmt.Sprintf("Search %s", pattern)
	if path != "" {
		title += " in " + path
	}
	return RenderIntent{Type: IntentLocations, Title: title}
}

func (l LocationsPresenter) PresentResult(result Result) RenderIntent {
	lines := strings.Split(result.Text, "\n")
	count := 0
	for _, line := range lines {
		if strings.Contains(line, ":") {
			count++
		}
	}
	return RenderIntent{
		Type:    IntentLocations,
		Summary: fmt.Sprintf("%d locations", count),
	}
}

// TablePresenter renders structured data as tables.
type TablePresenter struct{}

func (t TablePresenter) PresentCall(args map[string]any) RenderIntent {
	return RenderIntent{Type: IntentTable}
}

func (t TablePresenter) PresentResult(result Result) RenderIntent {
	return RenderIntent{Type: IntentTable, Summary: truncate(result.Text, 200)}
}

// ProgressPresenter renders long-running operations.
type ProgressPresenter struct {
	TotalSteps int
}

func (p ProgressPresenter) PresentCall(args map[string]any) RenderIntent {
	return RenderIntent{
		Type:  IntentProgress,
		Title: "Running...",
	}
}

func (p ProgressPresenter) PresentResult(result Result) RenderIntent {
	return RenderIntent{
		Type:    IntentProgress,
		Summary: truncate(result.Text, 100),
	}
}

// GetPresenter returns the appropriate presenter for a tool by name.
// Returns DefaultPresenter if no specialized presenter is registered.
func GetPresenter(toolName string) Presenter {
	switch toolName {
	case "edit_file", "apply_patch", "write_file":
		return DiffPresenter{}
	case "grep", "glob":
		return LocationsPresenter{}
	default:
		return DefaultPresenter{}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
