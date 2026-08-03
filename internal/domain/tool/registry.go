package tool

import "sync"

type MapRegistry struct {
	mu    sync.RWMutex
	tools map[string]ITool
}

func NewRegistry() *MapRegistry {
	return &MapRegistry{tools: make(map[string]ITool)}
}

func (r *MapRegistry) Register(t ITool) {
	if t == nil {
		return
	}
	r.mu.Lock()
	r.tools[t.Name()] = t
	r.mu.Unlock()
}

func (r *MapRegistry) Unregister(name string) {
	r.mu.Lock()
	delete(r.tools, name)
	r.mu.Unlock()
}

func (r *MapRegistry) Get(name string) ITool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
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

func (r *MapRegistry) Descriptions() []map[string]string {
	list := r.List()
	out := make([]map[string]string, 0, len(list))
	for _, t := range list {
		out = append(out, map[string]string{"name": t.Name(), "description": t.Description()})
	}
	return out
}
