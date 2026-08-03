package skill

// Skill package metadata + body (SKILL.md).
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers,omitempty"`
	Tools       []string `json:"tools,omitempty"`    // allowed tools when active (empty = all)
	Depends     []string `json:"depends,omitempty"`  // skill ids to compose (nested)
	Body        string   `json:"body,omitempty"`
	Path        string   `json:"path,omitempty"`
	Source      string   `json:"source,omitempty"`
	Enabled     bool     `json:"enabled"`
}
