package tool

import (
	"fmt"
	"strings"
	"sync"
)

type MapRegistry struct {
	mu       sync.RWMutex
	tools    map[string]ITool
	meta     map[string]ToolMetadata
	version  map[string]string
	depGraph *ToolDependencyGraph
}

func NewRegistry() *MapRegistry {
	return &MapRegistry{
		tools:    make(map[string]ITool),
		meta:     make(map[string]ToolMetadata),
		version:  make(map[string]string),
		depGraph: NewToolDependencyGraph(),
	}
}

func (r *MapRegistry) Register(t ITool) {
	if t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t

	if mt, ok := t.(IToolMeta); ok {
		meta := mt.Metadata()
		r.meta[t.Name()] = meta
		if meta.Version != "" {
			r.version[t.Name()] = meta.Version
		}
		for _, dep := range meta.DependsOn {
			r.depGraph.AddDependency(t.Name(), dep, "", true)
		}
	} else {
		r.meta[t.Name()] = DefaultMeta(t, r.inferCategory(t))
	}
}

func (r *MapRegistry) RegisterWithMeta(t ITool, meta ToolMetadata) {
	if t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	r.meta[t.Name()] = meta
	if meta.Version != "" {
		r.version[t.Name()] = meta.Version
	}
	for _, dep := range meta.DependsOn {
		r.depGraph.AddDependency(t.Name(), dep, "", true)
	}
}

func (r *MapRegistry) inferCategory(t ITool) ToolCategory {
	name := t.Name()
	if strings.HasPrefix(name, "read") || strings.HasPrefix(name, "get") {
		return CategoryRead
	}
	if strings.HasPrefix(name, "write") || strings.HasPrefix(name, "edit") || strings.HasPrefix(name, "create") {
		return CategoryWrite
	}
	if strings.HasPrefix(name, "search") || strings.HasPrefix(name, "find") || strings.HasPrefix(name, "grep") {
		return CategorySearch
	}
	if strings.HasPrefix(name, "exec") || strings.HasPrefix(name, "bash") || strings.HasPrefix(name, "run") {
		return CategoryExec
	}
	if strings.HasPrefix(name, "glob") || strings.HasPrefix(name, "list") {
		return CategoryGlob
	}
	if strings.HasPrefix(name, "plan") {
		return CategoryPlan
	}
	if strings.HasPrefix(name, "memory") {
		return CategoryMemory
	}
	if strings.HasPrefix(name, "web") {
		return CategoryWeb
	}
	return CategoryRead
}

func (r *MapRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	delete(r.meta, name)
	delete(r.version, name)
}

func (r *MapRegistry) Get(name string) ITool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

func (r *MapRegistry) GetInfo(name string) (ToolInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return ToolInfo{}, false
	}
	meta := r.meta[name]
	return ToolInfoFromMeta(t, meta), true
}

func (r *MapRegistry) List() []ITool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ITool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

func (r *MapRegistry) ListInfo() []ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolInfo, 0, len(r.tools))
	for name, t := range r.tools {
		meta := r.meta[name]
		out = append(out, ToolInfoFromMeta(t, meta))
	}
	return out
}

func (r *MapRegistry) ListByCategory(category ToolCategory) []ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolInfo
	for name, t := range r.tools {
		meta := r.meta[name]
		if meta.Category == category {
			out = append(out, ToolInfoFromMeta(t, meta))
		}
	}
	return out
}

func (r *MapRegistry) ListReadonly() []ToolInfo {
	return r.ListByCategory(CategoryRead)
}

func (r *MapRegistry) ListWritable() []ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolInfo
	for name, t := range r.tools {
		meta := r.meta[name]
		if !meta.IsReadOnly {
			out = append(out, ToolInfoFromMeta(t, meta))
		}
	}
	return out
}

func (r *MapRegistry) SearchTools(query string) []ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolInfo
	lower := strings.ToLower(query)
	for name, t := range r.tools {
		meta := r.meta[name]
		if meta.Matches(query) || strings.Contains(strings.ToLower(t.Description()), lower) {
			out = append(out, ToolInfoFromMeta(t, meta))
		}
	}
	return out
}

func (r *MapRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

func (r *MapRegistry) Categories() map[ToolCategory]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cats := make(map[ToolCategory]int)
	for _, meta := range r.meta {
		cats[meta.Category]++
	}
	return cats
}

func (r *MapRegistry) Descriptions() []map[string]string {
	list := r.List()
	out := make([]map[string]string, 0, len(list))
	for _, t := range list {
		meta := r.meta[t.Name()]
		entry := map[string]string{
			"name":        t.Name(),
			"description": t.Description(),
			"category":    string(meta.Category),
			"version":     meta.Version,
		}
		if meta.IsReadOnly {
			entry["readonly"] = "true"
		}
		if meta.IsCacheable {
			entry["cacheable"] = "true"
		}
		if meta.IsLongRun {
			entry["long_running"] = "true"
		}
		if meta.Deprecated {
			entry["deprecated"] = "true"
			if meta.Replica != "" {
				entry["replaced_by"] = meta.Replica
			}
		}
		if len(meta.Tags) > 0 {
			entry["tags"] = strings.Join(meta.Tags, ",")
		}
		out = append(out, entry)
	}
	return out
}

func (r *MapRegistry) DependencyGraph() *ToolDependencyGraph {
	return r.depGraph
}

func (r *MapRegistry) ValidateDependencies() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name := range r.tools {
		deps := r.depGraph.GetDependencies(name)
		for _, dep := range deps {
			if _, ok := r.tools[dep.Name]; !ok {
				return fmt.Errorf("tool %s depends on %s but it is not registered", name, dep.Name)
			}
		}
		if r.depGraph.HasCircularDependency(name) {
			return ErrCircularDependency
		}
	}
	return nil
}
