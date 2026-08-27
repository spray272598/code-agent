package tool

import (
	"fmt"
	"strings"
	"sync"
)

// Scope implements chain-inherited tool scoping inspired by DeepSeek Harness.
// A Scope wraps a parent scope and can add, override, or restrict tools.
// Lookups walk up the parent chain; local registrations shadow parents.
type Scope struct {
	mu       sync.RWMutex
	parent   *Scope
	tools    map[string]ITool
	meta     map[string]ToolMetadata
	allowSet map[string]bool // if non-nil, only these tools are visible (restrict mode)
	denySet  map[string]bool
}

// NewScope creates a root scope with no parent.
func NewScope() *Scope {
	return &Scope{
		tools: make(map[string]ITool),
		meta:  make(map[string]ToolMetadata),
	}
}

// Child creates a child scope that inherits from this scope.
// The child starts with an empty tool set; tools resolved via parent chain.
func (s *Scope) Child() *Scope {
	return &Scope{
		parent: s,
		tools:  make(map[string]ITool),
		meta:   make(map[string]ToolMetadata),
	}
}

// Register adds or overrides a tool in this scope (does not affect parent).
func (s *Scope) Register(t ITool) {
	if t == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name()] = t
	if mt, ok := t.(IToolMeta); ok {
		s.meta[t.Name()] = mt.Metadata()
	} else {
		s.meta[t.Name()] = DefaultMeta(t, CategoryRead)
	}
}

// Unregister removes a tool from this scope (does not affect parent).
func (s *Scope) Unregister(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tools, name)
	delete(s.meta, name)
}

// Get resolves a tool by walking up the scope chain.
// Local registrations shadow parent registrations.
func (s *Scope) Get(name string) ITool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.get(name)
}

func (s *Scope) get(name string) ITool {
	if s.allowSet != nil && !s.allowSet[name] {
		return nil
	}
	if s.denySet != nil && s.denySet[name] {
		return nil
	}
	if t, ok := s.tools[name]; ok {
		return t
	}
	if s.parent != nil {
		return s.parent.get(name)
	}
	return nil
}

// List returns all visible tools (local + inherited, deduplicated).
// Local tools shadow parent tools with the same name.
func (s *Scope) List() []ITool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	var result []ITool
	s.collectList(&result, seen)
	return result
}

func (s *Scope) collectList(out *[]ITool, seen map[string]bool) {
	if s.parent != nil {
		s.parent.collectList(out, seen)
	}
	for name, t := range s.tools {
		if s.allowSet != nil && !s.allowSet[name] {
			continue
		}
		if s.denySet != nil && s.denySet[name] {
			continue
		}
		if !seen[name] {
			seen[name] = true
			*out = append(*out, t)
		}
	}
}

// Restrict limits visible tools to the given allow list.
// Pass nil to clear restrictions.
func (s *Scope) Restrict(allow []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if allow == nil {
		s.allowSet = nil
		return
	}
	s.allowSet = make(map[string]bool, len(allow))
	for _, name := range allow {
		s.allowSet[name] = true
	}
}

// Deny explicitly blocks tools by name (checked after allow).
func (s *Scope) Deny(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if names == nil {
		s.denySet = nil
		return
	}
	s.denySet = make(map[string]bool, len(names))
	for _, name := range names {
		s.denySet[name] = true
	}
}

// Descriptions returns tool descriptions for all visible tools.
func (s *Scope) Descriptions() []map[string]string {
	list := s.List()
	out := make([]map[string]string, 0, len(list))
	for _, t := range list {
		out = append(out, map[string]string{
			"name":        t.Name(),
			"description": t.Description(),
		})
	}
	return out
}

// String returns a debug representation of the scope chain.
func (s *Scope) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	b.WriteString("Scope{")
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	b.WriteString(fmt.Sprintf("local=%v", names))
	if s.allowSet != nil {
		b.WriteString(fmt.Sprintf(" allow=%v", len(s.allowSet)))
	}
	if s.denySet != nil {
		b.WriteString(fmt.Sprintf(" deny=%v", len(s.denySet)))
	}
	if s.parent != nil {
		b.WriteString(" → ")
		b.WriteString(s.parent.String())
	}
	b.WriteString("}")
	return b.String()
}
