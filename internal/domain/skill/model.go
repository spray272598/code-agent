package skill

// Skill package metadata + body (SKILL.md).
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers,omitempty"`
	Tools       []string `json:"tools,omitempty"`    // allowed tools when active (empty = all)
	Depends     []string `json:"depends,omitempty"`  // skill ids to compose (nested)
	Author      string   `json:"author,omitempty"`   // marketplace / upload author
	Version     string   `json:"version,omitempty"`  // semver-ish, e.g. "1.2.0"
	Tags        []string `json:"tags,omitempty"`     // marketplace search facets
	Body        string   `json:"body,omitempty"`
	Path        string   `json:"path,omitempty"`
	Source      string   `json:"source,omitempty"` // "installed" | "market" | "draft"
	Enabled     bool     `json:"enabled"`
}

// SkillListing is a marketplace catalog entry (metadata only, decoupled from
// whether it is installed locally). Used by the skill marketplace (3.2).
type SkillListing struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Author      string   `json:"author,omitempty"`
	Version     string   `json:"version,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Source      string   `json:"source,omitempty"` // "market" | "draft"
	Installed   bool     `json:"installed"`
}
