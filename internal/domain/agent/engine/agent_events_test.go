package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestAgentEventBusEmit(t *testing.T) {
	bus := NewAgentEventBus()
	var received []string
	bus.On(AgentPreStep, func(ctx context.Context, payload any) error {
		received = append(received, "handler1")
		return nil
	})
	bus.On(AgentPreStep, func(ctx context.Context, payload any) error {
		received = append(received, "handler2")
		return nil
	})

	bus.Emit(context.Background(), AgentPreStep, nil)
	if len(received) != 2 {
		t.Fatalf("expected 2 handlers, got %d", len(received))
	}
	if received[0] != "handler1" || received[1] != "handler2" {
		t.Errorf("unexpected order: %v", received)
	}
}

func TestAgentEventBusEmitErrorLogged(t *testing.T) {
	bus := NewAgentEventBus()
	bus.On(AgentPreStep, func(ctx context.Context, payload any) error {
		return errors.New("boom")
	})
	// Should not panic
	bus.Emit(context.Background(), AgentPreStep, nil)
}

func TestAgentEventBusWaterfall(t *testing.T) {
	bus := NewAgentEventBus()
	var order []string

	bus.OnWaterfall(AgentRequest, func(ctx context.Context, payload any, next func(ctx context.Context) error) error {
		order = append(order, "pre")
		err := next(ctx)
		order = append(order, "post")
		return err
	})

	err := bus.Waterfall(context.Background(), AgentRequest, nil, func(ctx context.Context) error {
		order = append(order, "inner")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "pre" || order[1] != "inner" || order[2] != "post" {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestAgentEventBusWaterfallShortCircuit(t *testing.T) {
	bus := NewAgentEventBus()
	innerCalled := false

	bus.OnWaterfall(AgentRequest, func(ctx context.Context, payload any, next func(ctx context.Context) error) error {
		return errors.New("blocked")
	})

	err := bus.Waterfall(context.Background(), AgentRequest, nil, func(ctx context.Context) error {
		innerCalled = true
		return nil
	})
	if err == nil {
		t.Error("expected error from short-circuit")
	}
	if innerCalled {
		t.Error("inner should not be called when short-circuited")
	}
}

func TestAgentEventBusWaterfallNoHandlers(t *testing.T) {
	bus := NewAgentEventBus()
	innerCalled := false

	err := bus.Waterfall(context.Background(), AgentRequest, nil, func(ctx context.Context) error {
		innerCalled = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !innerCalled {
		t.Error("inner should be called when no handlers")
	}
}

func TestAgentEventBusWaterfallChained(t *testing.T) {
	bus := NewAgentEventBus()
	var order []string

	bus.OnWaterfall(AgentRequest, func(ctx context.Context, payload any, next func(ctx context.Context) error) error {
		order = append(order, "outer-pre")
		err := next(ctx)
		order = append(order, "outer-post")
		return err
	})
	bus.OnWaterfall(AgentRequest, func(ctx context.Context, payload any, next func(ctx context.Context) error) error {
		order = append(order, "inner-pre")
		err := next(ctx)
		order = append(order, "inner-post")
		return err
	})

	err := bus.Waterfall(context.Background(), AgentRequest, nil, func(ctx context.Context) error {
		order = append(order, "core")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"outer-pre", "inner-pre", "core", "inner-post", "outer-post"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d, got %d: %v", len(expected), len(order), order)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Errorf("index %d: expected %s, got %s", i, expected[i], order[i])
		}
	}
}

func TestAgentEventBusHasListeners(t *testing.T) {
	bus := NewAgentEventBus()
	if bus.HasListeners(AgentPreStep) {
		t.Error("should have no listeners initially")
	}
	bus.On(AgentPreStep, func(ctx context.Context, payload any) error { return nil })
	if !bus.HasListeners(AgentPreStep) {
		t.Error("should have listeners after On()")
	}
}

func TestAgentEventBusConcurrent(t *testing.T) {
	bus := NewAgentEventBus()
	var mu sync.Mutex
	var count int

	for i := 0; i < 10; i++ {
		bus.On(AgentPreStep, func(ctx context.Context, payload any) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		})
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit(context.Background(), AgentPreStep, nil)
		}()
	}
	wg.Wait()
	if count != 1000 {
		t.Errorf("expected 1000, got %d", count)
	}
}
