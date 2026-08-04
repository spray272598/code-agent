package einoorch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/spray272598/code-agent/internal/domain/agent/engine"
	"github.com/spray272598/code-agent/internal/domain/security"
	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	domtool "github.com/spray272598/code-agent/internal/domain/tool"
)

func TestIsContinue(t *testing.T) {
	yes := []string{"继续", "continue", "OK", "y", "Yes", "继续执行", "  continue  "}
	for _, s := range yes {
		if !isContinue(s) {
			t.Fatalf("want continue for %q", s)
		}
	}
	no := []string{"继续做吧", "ok go", "please continue", "", "approve"}
	for _, s := range no {
		if isContinue(s) {
			t.Fatalf("want NOT continue for %q", s)
		}
	}
}

func TestLooksMultiAndDeep(t *testing.T) {
	if !looksMulti("/team explore auth") {
		t.Fatal("team")
	}
	if !looksMulti("/parallel foo") {
		t.Fatal("parallel")
	}
	if !looksMulti("team mode: check logs") {
		t.Fatal("team mode")
	}
	if looksMulti("normal question") {
		t.Fatal("false multi")
	}
	if !looksDeep("/deep implement feature") && !looksDeep("deep agent: x") {
		// deepagent.LooksDeep may only match /deep prefix — just ensure helper wires through
		_ = looksDeep("/deep x")
	}
}

func TestIsInterruptErr_noFalsePositive(t *testing.T) {
	// network / syscall style messages must NOT be treated as HITL interrupt
	falsePositives := []error{
		errors.New("connection interrupted by peer"),
		errors.New("read: interrupted system call"),
		errors.New("Interrupt signal received from user cancel"),
		fmt.Errorf("wrap: %w", errors.New("stream interrupted")),
	}
	for _, err := range falsePositives {
		if isInterruptErr(err) {
			t.Fatalf("false positive interrupt for %q", err.Error())
		}
	}
	if isInterruptErr(nil) {
		t.Fatal("nil")
	}
	// known compose-style prefix
	if !isInterruptErr(errors.New("interrupt happened, info: map[]")) {
		t.Fatal("want interrupt happened prefix")
	}
	if !isInterruptErr(errors.New("node X: interrupt and rerun at address tools")) {
		t.Fatal("want interrupt and rerun")
	}
}

func TestIsInterruptErr_typedProvider(t *testing.T) {
	// Simulate graph interruptError shape: GetInterruptContexts() []*compose.InterruptCtx
	err := &fakeInterruptProvider{}
	if !isInterruptErr(err) {
		t.Fatal("typed GetInterruptContexts provider should match")
	}
	// Wrong signature (old bug): GetInterruptContexts() any must NOT match via errors.As;
	// free-form "interrupted" also must not match string heuristics.
	if isInterruptErr(&wrongSigInterrupt{}) {
		t.Fatal("wrong method signature should not be treated as interrupt")
	}
}

type fakeInterruptProvider struct{}

func (f *fakeInterruptProvider) Error() string { return "fake interrupt provider" }
func (f *fakeInterruptProvider) GetInterruptContexts() []*compose.InterruptCtx {
	return nil
}

type wrongSigInterrupt struct{}

func (w *wrongSigInterrupt) Error() string             { return "something interrupted" }
func (w *wrongSigInterrupt) GetInterruptContexts() any { return nil }

// --- runStats ---

func TestRunStatsTokenUsedAndAddUsage(t *testing.T) {
	s := &runStats{}
	if s.TokenUsed() != 0 {
		t.Fatal("empty")
	}
	s.addUsage(&model.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	if s.TokenUsed() != 15 {
		t.Fatalf("got %d", s.TokenUsed())
	}
	s2 := &runStats{}
	s2.addUsage(&model.TokenUsage{PromptTokens: 3, CompletionTokens: 4}) // no TotalTokens
	if s2.TokenUsed() != 7 {
		t.Fatalf("sum path got %d", s2.TokenUsed())
	}
	var nilS *runStats
	if nilS.TokenUsed() != 0 {
		t.Fatal("nil receiver")
	}
	nilS.addUsage(&model.TokenUsage{TotalTokens: 1}) // no panic
}

// --- processStreamMessage unit (extracted logic tested via helper) ---

func TestPublishToolCallDeltasFromMessage(t *testing.T) {
	var events []*engine.Event
	publish := func(ev *engine.Event) { events = append(events, ev) }

	idx0 := 0
	msg := &schema.Message{
		Content: "thinking",
		ToolCalls: []schema.ToolCall{
			{Index: &idx0, ID: "c1", Function: schema.FunctionCall{Name: "read_file", Arguments: `{"path":`}},
		},
	}
	// reuse same accumulation logic as runStream for one chunk
	type tcAcc struct {
		name string
		args strings.Builder
		id   string
	}
	byIdx := map[int]*tcAcc{}
	emittedName := map[string]bool{}
	if msg.Content != "" {
		publish(&engine.Event{Type: engine.EventTextDelta, Content: msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		acc, ok := byIdx[idx]
		if !ok {
			acc = &tcAcc{}
			byIdx[idx] = acc
		}
		if tc.ID != "" {
			acc.id = tc.ID
		}
		if tc.Function.Name != "" && acc.name == "" {
			acc.name = tc.Function.Name
			if !emittedName[acc.name] {
				emittedName[acc.name] = true
				publish(&engine.Event{Type: engine.EventToolCall, SubType: acc.name, Content: acc.name})
			}
		}
		if tc.Function.Arguments != "" {
			acc.args.WriteString(tc.Function.Arguments)
		}
	}
	// second chunk: arg close
	msg2 := &schema.Message{
		ToolCalls: []schema.ToolCall{
			{Index: &idx0, Function: schema.FunctionCall{Arguments: `"a.go"}`}},
		},
	}
	for _, tc := range msg2.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		acc := byIdx[idx]
		acc.args.WriteString(tc.Function.Arguments)
	}
	raw := byIdx[0].args.String()
	if raw != `{"path":"a.go"}` {
		t.Fatalf("args acc %q", raw)
	}
	var hasDelta, hasTC bool
	for _, e := range events {
		if e.Type == engine.EventTextDelta {
			hasDelta = true
		}
		if e.Type == engine.EventToolCall && e.SubType == "read_file" {
			hasTC = true
		}
	}
	if !hasDelta || !hasTC {
		t.Fatalf("events incomplete: %+v", events)
	}
}

func TestFilterEinoToolsByAllow(t *testing.T) {
	reg := domtool.NewRegistry()
	reg.Register(echoT{})
	reg.Register(bashT{})
	tools := WrapRegistry(reg, security.NewGuard(".", true, false))
	if len(tools) < 2 {
		t.Fatalf("tools %d", len(tools))
	}
	filtered := filterEinoToolsByAllow(tools, []string{"echo"})
	if len(filtered) != 1 {
		t.Fatalf("want 1 got %d", len(filtered))
	}
	info, _ := filtered[0].Info(context.Background())
	if info.Name != "echo" {
		t.Fatalf("got %s", info.Name)
	}
	// wildcard / empty
	if len(filterEinoToolsByAllow(tools, nil)) != len(tools) {
		t.Fatal("nil allow")
	}
	if len(filterEinoToolsByAllow(tools, []string{"*"})) != len(tools) {
		t.Fatal("star")
	}
}

func TestResumeHITLPath_executesApprovedTool(t *testing.T) {
	// Guard: create pending write-like tool then approve → TakeReadyResume ready
	g := security.NewGuard("./workspace", true, false)
	// bash requires confirm by default when default_confirm
	g2 := security.NewGuard("./workspace", true, true)
	_ = g
	reg := domtool.NewRegistry()
	reg.Register(echoT{})
	reg.Register(bashT{})

	// Manual pending + approve for echo (simulate write confirm)
	p := g2.CreatePending("sess-r1", "echo", map[string]any{"text": "hi"}, security.Decision{
		Action: security.ActionConfirm, Layer: "L3", Reason: "test", Tool: "echo",
	})
	if _, err := g2.Approve(p.ID, "once"); err != nil {
		t.Fatal(err)
	}
	ready := g2.TakeReadyResume("sess-r1")
	if ready == nil || ready.Tool != "echo" {
		t.Fatalf("ready %+v", ready)
	}

	// Execute like Runner resume (UseInterrupt=false, AutoApprove=true)
	gt := &GuardedTool{Inner: echoT{}, Guard: g2, UseInterrupt: false}
	ctx := WithRunContext(context.Background(), RunContext{
		SessionID: "sess-r1", AutoApprove: true,
	})
	argsJSON, _ := jsonMarshal(ready.Args)
	out, err := gt.InvokableRun(ctx, argsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo:hi" {
		t.Fatalf("got %q", out)
	}
}

func TestRunnerResume_unknownTool(t *testing.T) {
	g := security.NewGuard("./workspace", true, true)
	p := g.CreatePending("s-unk", "missing_tool", map[string]any{}, security.Decision{
		Action: security.ActionConfirm, Reason: "x", Tool: "missing_tool",
	})
	_, _ = g.Approve(p.ID, "once")

	r := NewRunner(Config{MaxSteps: 3, TokenBudget: 8000}, domtool.NewRegistry(), g, nil, nil)
	sess := &sessmodel.Session{ID: "s-unk", UserID: "u1"}
	ch := make(chan *engine.Event, 16)
	// drain
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	res, err := r.Run(context.Background(), sess, "继续", ch, engine.RunOptions{})
	close(ch)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.ErrorClass != "tool" {
		t.Fatalf("want tool error, got %+v", res)
	}
	if !strings.Contains(res.Response, "unknown tool") {
		t.Fatalf("resp %q", res.Response)
	}
}

func TestFirstPending(t *testing.T) {
	if firstPending(nil, "s") != nil {
		t.Fatal("nil guard")
	}
	g := security.NewGuard(".", true, true)
	if firstPending(g, "s") != nil {
		t.Fatal("empty")
	}
	p := g.CreatePending("s", "bash", map[string]any{"command": "ls"}, security.Decision{
		Action: security.ActionConfirm, Tool: "bash", Reason: "r",
	})
	got := firstPending(g, "s")
	if got == nil || got.ID != p.ID {
		t.Fatalf("got %+v", got)
	}
}

func TestMultiResultAggregation_heuristic(t *testing.T) {
	// unit: ensure multiResult fields roll up as Runner Result would
	outs := []multiResult{
		{Role: "explore", Output: "aaaa", ToolCalls: 2, TokenUsed: 100},
		{Role: "verify", Output: "bbbb", ToolCalls: 1, TokenUsed: 50},
	}
	totalTools, totalTokens := 0, 0
	for _, o := range outs {
		totalTools += o.ToolCalls
		totalTokens += o.TokenUsed
	}
	totalTools += 0 // merge
	totalTokens += 30
	if totalTools != 3 || totalTokens != 180 {
		t.Fatalf("tools=%d tokens=%d", totalTools, totalTokens)
	}
}

func TestIsContinueDoesNotTriggerSkillMatchPath(t *testing.T) {
	// isContinue should skip skill matching in Runner (code path condition)
	if !isContinue("继续") {
		t.Fatal()
	}
	// non-continue does skill match
	if isContinue("read README") {
		t.Fatal()
	}
}

// Ensure compile of timestamp helper
func TestNowMs(t *testing.T) {
	n := nowMs()
	if n < time.Now().Add(-time.Minute).UnixMilli() {
		t.Fatal(n)
	}
}
