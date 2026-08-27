package plugin

import (
	"errors"
	"testing"
)

type mockPlugin struct {
	name    string
	deps    []string
	started bool
	stopped bool
}

func (m *mockPlugin) Name() string               { return m.name }
func (m *mockPlugin) Dependencies() []string     { return m.deps }
func (m *mockPlugin) Register(_ *Registry) error { return nil }
func (m *mockPlugin) Start(_ Context) error      { m.started = true; return nil }
func (m *mockPlugin) Stop() error                { m.stopped = true; return nil }

func TestRegistryRegister(t *testing.T) {
	r := NewRegistry()
	p := &mockPlugin{name: "test"}
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("test")
	if !ok || got.Name() != "test" {
		t.Error("plugin not found")
	}
}

func TestRegistryDuplicate(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockPlugin{name: "test"})
	if err := r.Register(&mockPlugin{name: "test"}); err == nil {
		t.Error("expected duplicate error")
	}
}

func TestRegistryStartOrder(t *testing.T) {
	r := NewRegistry()
	var order []string
	r.Register(&mockPlugin{name: "a", deps: []string{"b"}})
	r.Register(&mockPlugin{name: "b", deps: nil})

	// Replace with plugins that track order
	r.plugins["a"] = &entry{
		plugin: &orderTracker{name: "a", deps: []string{"b"}, order: &order},
		status: StatusRegistered,
	}
	r.plugins["b"] = &entry{
		plugin: &orderTracker{name: "b", deps: nil, order: &order},
		status: StatusRegistered,
	}

	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Errorf("wrong order: %v", order)
	}
}

func TestRegistryDependencyFailure(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockPlugin{name: "a", deps: []string{"missing"}})
	if err := r.Start(); err == nil {
		t.Error("expected error for missing dependency")
	}
}

func TestRegistryStartFailure(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockPlugin{name: "a", deps: nil})
	r.Register(&mockPlugin{name: "b", deps: []string{"a"}})

	// Replace a with a failing plugin
	r.plugins["a"] = &entry{
		plugin: &failPlugin{name: "a"},
		status: StatusRegistered,
	}

	if err := r.Start(); err == nil {
		t.Error("expected error from failing plugin")
	}
	if r.Status("b") != StatusFailed {
		t.Errorf("b should be failed, got %s", r.Status("b"))
	}
}

func TestRegistryStop(t *testing.T) {
	r := NewRegistry()
	a := &mockPlugin{name: "a"}
	b := &mockPlugin{name: "b"}
	r.Register(a)
	r.Register(b)
	r.Start()
	r.Stop()
	if !a.stopped || !b.stopped {
		t.Error("plugins not stopped")
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()
	p := &mockPlugin{name: "test"}
	r.Register(p)
	r.Start()
	if err := r.Unregister("test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("test"); ok {
		t.Error("plugin should be removed")
	}
	if !p.stopped {
		t.Error("plugin should be stopped before unregister")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockPlugin{name: "a"})
	r.Register(&mockPlugin{name: "b"})
	r.Start()
	list := r.List()
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
	if list["a"] != StatusRunning {
		t.Errorf("a should be running, got %s", list["a"])
	}
}

type orderTracker struct {
	name  string
	deps  []string
	order *[]string
}

func (o *orderTracker) Name() string               { return o.name }
func (o *orderTracker) Dependencies() []string     { return o.deps }
func (o *orderTracker) Register(_ *Registry) error { return nil }
func (o *orderTracker) Start(_ Context) error {
	*o.order = append(*o.order, o.name)
	return nil
}
func (o *orderTracker) Stop() error { return nil }

type failPlugin struct {
	name string
}

func (f *failPlugin) Name() string               { return f.name }
func (f *failPlugin) Dependencies() []string     { return nil }
func (f *failPlugin) Register(_ *Registry) error { return nil }
func (f *failPlugin) Start(_ Context) error      { return errors.New("deliberate failure") }
func (f *failPlugin) Stop() error                { return nil }
