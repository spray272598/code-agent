package agent

import (
	"context"
	"testing"
)

func TestWaterfallBasic(t *testing.T) {
	wf := NewWaterfall()
	var order []int
	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
		order = append(order, 1)
		return w.Next(ctx)
	})
	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
		order = append(order, 2)
		return w.Next(ctx)
	})
	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
		order = append(order, 3)
		return nil
	})
	err := wf.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestWaterfallAbort(t *testing.T) {
	wf := NewWaterfall()
	var order []int
	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
		order = append(order, 1)
		w.Abort()
		return w.Next(ctx)
	})
	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
		order = append(order, 2)
		return w.Next(ctx)
	})
	wf.Run(context.Background(), nil)
	if len(order) != 1 {
		t.Errorf("expected only first handler, got %v", order)
	}
}

func TestWaterfallDataMutation(t *testing.T) {
	wf := NewWaterfall()
	wf.Use(func(ctx context.Context, w *WaterfallContext) error {
		d := w.Data.(*ToolExecData)
		d.Result = "modified"
		return w.Next(ctx)
	})
	data := &ToolExecData{ToolName: "test"}
	err := wf.Run(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if data.Result != "modified" {
		t.Errorf("expected modified result, got %q", data.Result)
	}
}

func TestWaterfallEmpty(t *testing.T) {
	wf := NewWaterfall()
	err := wf.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGuardWaterfall(t *testing.T) {
	called := false
	gw := NewGuardWaterfall(func(ctx context.Context, w *WaterfallContext) error {
		called = true
		d, ok := w.Data.(*ToolExecData)
		if !ok {
			t.Fatalf("unexpected data type: %T", w.Data)
		}
		if d.ToolName == "bash" {
			d.Deny = true
			d.DenyMsg = "bash denied in strict mode"
			w.Abort()
			return nil
		}
		return w.Next(ctx)
	})

	data := &ToolExecData{ToolName: "bash"}
	if gw.Check(context.Background(), data) {
		t.Error("bash should be denied")
	}
	if !called {
		t.Error("interceptor was not called")
	}

	data2 := &ToolExecData{ToolName: "grep"}
	if !gw.Check(context.Background(), data2) {
		t.Error("grep should be allowed")
	}
}
