package hook

import (
	"context"
	"testing"
)

func TestPreToolUseAbort(t *testing.T) {
	b := NewBus()
	b.On(PreToolUse, func(ctx context.Context, ev Event) error {
		if ev.Tool == "bash" {
			return Abort("blocked by policy")
		}
		return nil
	})
	aborted, err := b.EmitCheck(context.Background(), Event{Point: PreToolUse, Tool: "bash"})
	if !aborted || !IsAbort(err) {
		t.Fatalf("want abort, got aborted=%v err=%v", aborted, err)
	}
	aborted, err = b.EmitCheck(context.Background(), Event{Point: PreToolUse, Tool: "read_file"})
	if aborted || err != nil {
		t.Fatalf("want allow, got %v %v", aborted, err)
	}
}
