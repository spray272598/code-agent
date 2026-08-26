package subagent

import (
	"regexp"
	"strings"
)

// ToolKind identifies a tool category for prompt placeholders.
type ToolKind int

const (
	ToolKindRead ToolKind = iota
	ToolKindListDir
	ToolKindSearch
	ToolKindWrite
	ToolKindEdit
	ToolKindExecute
	ToolKindWebSearch
	ToolKindWebFetch
)

// RoleCapability identifies a minimum toolset requirement for a role.
type RoleCapability int

const (
	RoleCapabilityNone       RoleCapability = iota
	RoleCapabilitySkeptic                   // Needs read + search
	RoleCapabilityStrategist                // Needs read + search + execute
)

// IsSatisfied checks whether this capability level is satisfied by the given
// tool name set. Returns false if required tools are missing or unsafe.
func (c RoleCapability) IsSatisfied(read, search, execute string) bool {
	switch c {
	case RoleCapabilitySkeptic:
		return read != "" && search != ""
	case RoleCapabilityStrategist:
		return read != "" && search != "" && execute != ""
	default:
		return true
	}
}

// RoleToolNames holds the resolved tool names for a role's prompt placeholders.
type RoleToolNames struct {
	Read      string
	List      string
	Search    string
	Write     string
	Edit      string
	Execute   string
	WebSearch string
	WebFetch  string
	Toolset   string
}

// Default fallback tool names (conservative, safe identifiers).
const (
	defaultRead      = "read_file"
	defaultList      = "list_dir"
	defaultSearch    = "grep"
	defaultWrite     = "write_file"
	defaultEdit      = "edit_file"
	defaultExecute   = "bash"
	defaultWebSearch = "web_search"
	defaultWebFetch  = "web_fetch"
)

// NewRoleToolNames builds RoleToolNames from explicit tool names.
// Any empty or unsafe name is replaced with its conservative default.
func NewRoleToolNames(read, list, search, write, edit, execute, webSearch, webFetch string) *RoleToolNames {
	return &RoleToolNames{
		Read:      sanitizedOrDefault(read, defaultRead),
		List:      sanitizedOrDefault(list, defaultList),
		Search:    sanitizedOrDefault(search, defaultSearch),
		Write:     sanitizedOrDefault(write, defaultWrite),
		Edit:      sanitizedOrDefault(edit, defaultEdit),
		Execute:   sanitizedOrDefault(execute, defaultExecute),
		WebSearch: sanitizedOrDefault(webSearch, defaultWebSearch),
		WebFetch:  sanitizedOrDefault(webFetch, defaultWebFetch),
	}
}

// FromInherit builds a RoleToolNames set by inheriting from a parent
// set. Missing keys fall back to the parent's values, which themselves
// fall back to conservative defaults.
func (rtn *RoleToolNames) FromInherit(inherit *RoleToolNames) *RoleToolNames {
	if inherit == nil {
		return rtn
	}
	if rtn.Read == "" {
		rtn.Read = sanitizedOrDefault(inherit.Read, defaultRead)
	}
	if rtn.List == "" {
		rtn.List = sanitizedOrDefault(inherit.List, defaultList)
	}
	if rtn.Search == "" {
		rtn.Search = sanitizedOrDefault(inherit.Search, defaultSearch)
	}
	if rtn.Write == "" {
		rtn.Write = sanitizedOrDefault(inherit.Write, defaultWrite)
	}
	if rtn.Edit == "" {
		rtn.Edit = sanitizedOrDefault(inherit.Edit, defaultEdit)
	}
	if rtn.Execute == "" {
		rtn.Execute = sanitizedOrDefault(inherit.Execute, defaultExecute)
	}
	if rtn.WebSearch == "" {
		rtn.WebSearch = sanitizedOrDefault(inherit.WebSearch, defaultWebSearch)
	}
	if rtn.WebFetch == "" {
		rtn.WebFetch = sanitizedOrDefault(inherit.WebFetch, defaultWebFetch)
	}
	return rtn
}

// DefaultToolNames returns RoleToolNames using all conservative defaults.
func DefaultToolNames() *RoleToolNames {
	return NewRoleToolNames("", "", "", "", "", "", "", "")
}

// DefaultToolNamesWithCap returns default tool names that satisfy the
// given role capability requirement.
func DefaultToolNamesWithCap(cap RoleCapability) *RoleToolNames {
	tn := DefaultToolNames()
	if !cap.IsSatisfied(tn.Read, tn.Search, tn.Execute) {
		// Should never happen with defaults, but be safe.
		return DefaultToolNames()
	}
	return tn
}

// Apply substitutes placeholders in a template.
// Placeholders: {READ_TOOL}, {LIST_TOOL}, {SEARCH_TOOL}, {WRITE_TOOL},
// {EXECUTE_TOOL}, {WEB_SEARCH_TOOL}, {WEB_FETCH_TOOL}, {TOOLSET_TOOLS}
//
// The substitution is a single left-to-right pass; resolved values are never
// rescanned so a resolved name cannot be re-expanded. Unknown tokens pass
// through untouched.
func (rtn *RoleToolNames) Apply(template string) string {
	resolve := func(token string) string {
		switch token {
		case "READ_TOOL":
			return rtn.Read
		case "LIST_TOOL":
			return rtn.List
		case "SEARCH_TOOL":
			return rtn.Search
		case "WRITE_TOOL":
			return rtn.Write
		case "EXECUTE_TOOL":
			return rtn.Execute
		case "WEB_SEARCH_TOOL":
			return rtn.WebSearch
		case "WEB_FETCH_TOOL":
			return rtn.WebFetch
		case "TOOLSET_TOOLS":
			return rtn.Toolset
		default:
			return ""
		}
	}
	var b strings.Builder
	b.Grow(len(template) + 64)
	rest := template
	for {
		open := strings.Index(rest, "{")
		if open < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		after := rest[open+1:]
		close := strings.Index(after, "}")
		if close < 0 {
			b.WriteString("{")
			b.WriteString(after)
			break
		}
		name := after[:close]
		value := resolve(name)
		if value != "" {
			b.WriteString(value)
		} else {
			b.WriteString("{")
			b.WriteString(after[:close+1])
		}
		rest = after[close+1:]
	}
	return b.String()
}

// SetToolset sets the {TOOLSET_TOOLS} block content.
func (rtn *RoleToolNames) SetToolset(block string) *RoleToolNames {
	rtn.Toolset = block
	return rtn
}

// IsSafe reports whether the tool name set passes all security checks:
// non-empty, all names match the safe charset, and minimum capability
// requirements are met for the given role.
func (rtn *RoleToolNames) IsSafe(cap RoleCapability) bool {
	if !cap.IsSatisfied(rtn.Read, rtn.Search, rtn.Execute) {
		return false
	}
	allNames := []string{rtn.Read, rtn.List, rtn.Search, rtn.Write, rtn.Edit, rtn.Execute, rtn.WebSearch, rtn.WebFetch}
	for _, n := range allNames {
		if !isSafeToolName(n) {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of the tool names.
func (rtn *RoleToolNames) Clone() *RoleToolNames {
	c := *rtn
	return &c
}

// firstSafeCandidate picks the first safe tool name from a list of candidates.
// Returns the default if all candidates are empty or unsafe.
func firstSafeCandidate(candidates []string, def string) string {
	for _, c := range candidates {
		if c = strings.TrimSpace(c); c != "" && isSafeToolName(c) {
			return c
		}
	}
	return def
}

// Safe charset for tool identifiers: lowercase alphanumeric + underscore,
// must start with a letter or underscore, 3-64 chars.
var safeToolNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_]{2,63}$`)

// isSafeToolName checks if a tool identifier matches the safe charset.
func isSafeToolName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	return safeToolNameRE.MatchString(name)
}

// sanitizedToolName matches conservative tool-id charset (alphanumeric + _ - .).
// This is a legacy, more permissive version for backward compatibility.
var toolNameRE = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

func sanitizedOrDefault(name, def string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return def
	}
	if !toolNameRE.MatchString(name) {
		return def
	}
	return name
}
