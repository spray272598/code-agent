package skill

import "errors"

// Skill package metadata + body (SKILL.md).
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers,omitempty"`
	Tools       []string `json:"tools,omitempty"`   // allowed tools when active (empty = all)
	Depends     []string `json:"depends,omitempty"` // skill ids to compose (nested)
	Author      string   `json:"author,omitempty"`  // marketplace / upload author
	Version     string   `json:"version,omitempty"` // semver-ish, e.g. "1.2.0"
	Tags        []string `json:"tags,omitempty"`    // marketplace search facets
	Body        string   `json:"body,omitempty"`
	Path        string   `json:"path,omitempty"`
	Source      string   `json:"source,omitempty"` // "installed" | "market" | "draft"
	Enabled     bool     `json:"enabled"`

	// New fields (T4 enhancement):
	ArgumentHint    string              `json:"argumentHint,omitempty"`      // e.g. "file path"
	UserInvocable   *bool               `json:"userInvocable,omitempty"`     // allow /skill by user
	DisableModelInv *bool               `json:"disableModelInvoc,omitempty"` // block LLM invocation
	Model           string              `json:"model,omitempty"`             // preferred LLM model
	Effort          string              `json:"effort,omitempty"`            // low | medium | high
	License         string              `json:"license,omitempty"`           // e.g. "MIT"
	Compatibility   string              `json:"compatibility,omitempty"`     // required deps description
	Metadata        map[string]string   `json:"metadata,omitempty"`          // arbitrary KV
	BlockTools      *BlockedToolsConfig `json:"blockTools,omitempty"`        // hard-block tools when skill active
}

// Errors returned by the skill system.
var (
	ErrSkillNotFound  = errors.New("skill not found")
	ErrSkillCycle     = errors.New("skill dependency cycle detected")
	ErrInvalidVersion = errors.New("invalid skill version")
)

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
