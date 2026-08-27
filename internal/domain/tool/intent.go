package tool

// RenderIntent describes how a tool call or result should be presented in a UI.
// Tools can override these to enable smart rendering in Web/terminal UIs.

// IntentType identifies the rendering strategy.
type IntentType string

const (
	IntentTerminal  IntentType = "terminal"  // plain text (default)
	IntentDiff      IntentType = "diff"      // unified diff / code change
	IntentLocations IntentType = "locations" // file locations (path:line)
	IntentTable     IntentType = "table"     // structured table
	IntentProgress  IntentType = "progress"  // progress indicator
	IntentImage     IntentType = "image"     // image / diagram
)

// RenderIntent is the metadata for smart UI rendering.
type RenderIntent struct {
	Type    IntentType     `json:"type"`
	Title   string         `json:"title,omitempty"`
	Summary string         `json:"summary,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// Presenter is an optional interface that tools can implement
// to control how their calls and results are rendered in the UI.
// Tools that don't implement this get IntentTerminal (plain text).
type Presenter interface {
	PresentCall(args map[string]any) RenderIntent
	PresentResult(result Result) RenderIntent
}

// DefaultPresenter returns terminal rendering for both call and result.
type DefaultPresenter struct{}

func (d DefaultPresenter) PresentCall(args map[string]any) RenderIntent {
	return RenderIntent{Type: IntentTerminal}
}

func (d DefaultPresenter) PresentResult(result Result) RenderIntent {
	return RenderIntent{Type: IntentTerminal}
}
