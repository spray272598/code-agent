package orchestration

import (
	"context"
	"testing"
	"time"

	sessmodel "github.com/spray272598/code-agent/internal/domain/session/model"
	sessrepo "github.com/spray272598/code-agent/internal/domain/session/adapter/repository"
)

func TestJournalAppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJournal(dir, "test-run-1")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	runID := "test-run-1"
	if err := j.LogStartRun(runID, "build a feature", 4); err != nil {
		t.Fatalf("LogStartRun: %v", err)
	}
	if err := j.LogPhaseCompletion(runID, "plan", "Step 1: Analyze"); err != nil {
		t.Fatalf("LogPhaseCompletion: %v", err)
	}
	if err := j.LogTokenUse(runID, 500); err != nil {
		t.Fatalf("LogTokenUse: %v", err)
	}
	if err := j.LogPhaseCompletion(runID, "act", "Step 2: Execute"); err != nil {
		t.Fatalf("LogPhaseCompletion: %v", err)
	}
	if err := j.LogComplete(runID, "Done"); err != nil {
		t.Fatalf("LogComplete: %v", err)
	}

	state := j.Replay(runID)
	if state.Status != StatusCompleted {
		t.Errorf("status = %s, want completed", state.Status)
	}
	if state.TokensUsed != 500 {
		t.Errorf("TokensUsed = %d, want 500", state.TokensUsed)
	}
	if len(state.PhasesDone) != 2 {
		t.Errorf("PhasesDone len = %d, want 2", len(state.PhasesDone))
	}
	if state.Results["plan"] != "Step 1: Analyze" {
		t.Errorf("plan result = %q", state.Results["plan"])
	}

	if !IsResumable(StatusPaused) {
		t.Error("StatusPaused should be resumable")
	}
	if !IsResumable(StatusInterrupted) {
		t.Error("StatusInterrupted should be resumable")
	}
	if !IsResumable(StatusFailed) {
		t.Error("StatusFailed should be resumable")
	}
	if IsResumable(StatusCompleted) {
		t.Error("StatusCompleted should not be resumable")
	}
	if IsResumable(StatusRunning) {
		t.Error("StatusRunning should not be resumable")
	}
}

func TestJournalEphemeral(t *testing.T) {
	j := NewEphemeralJournal()
	if err := j.LogStartRun("r1", "goal", 4); err != nil {
		t.Errorf("ephemeral append should not fail: %v", err)
	}
	if j.Path() != "" {
		t.Error("ephemeral journal should have empty path")
	}
	if j.Replay("r1") == nil {
		t.Error("replay should return non-nil state even for ephemeral")
	}
}

func TestJournalCancelStatus(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJournal(dir, "cancel-run")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	defer j.Close()

	runID := "cancel-run"
	_ = j.LogStartRun(runID, "do thing", 2)
	_ = j.LogCancel(runID, "user cancelled")
	state := j.Replay(runID)
	if state.Status != StatusCancelled {
		t.Errorf("status = %s, want cancelled", state.Status)
	}
}

func TestJournalFormatEntry(t *testing.T) {
	e := JournalEntry{
		Type: EntryPhase, RunID: "r1", PhaseID: "plan", Agent: "explore",
	}
	s := FormatEntry(e)
	if s == "" {
		t.Error("FormatEntry should produce non-empty string")
	}
}

func TestStatusFromString(t *testing.T) {
	if StatusFromString("running") != StatusRunning {
		t.Error("running status parse failed")
	}
	if StatusFromString("invalid_status_xyz") != StatusInit {
		t.Error("unknown status should default to init")
	}
}

func TestOrchestratorRouterDecideAuto(t *testing.T) {
	r := NewRouter()

	tests := []struct {
		input string
		want  OrchestratorMode
	}{
		{"compare the performance of Redis and Memcached for our cache", ModeTeams},
		{"implement a new feature with step by step plan", ModeDeepAgent},
		{"what is a goroutine", ModeSingleAgent},
		{"find all usages of the deprecated API and fix them", ModeTeams},
		{"plan a migration from MySQL to PostgreSQL, then do it", ModeDeepAgent},
		{"how to refactor the code, test it, and deploy", ModeDeepAgent},
		{"investigate the bug, audit the code, and write a fix", ModeTeams},
		{"explain how Redis caching works", ModeSingleAgent},
		{"compare vs contrast two solutions, analyze both", ModeTeams},
		{"review the code, run tests, and provide summary", ModeTeams},
	}

	for _, tt := range tests {
		got := r.DecideAuto(tt.input)
		if got != tt.want {
			t.Errorf("DecideAuto(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestOrchestratorRouterExplicit(t *testing.T) {
	r := NewRouter()
	if got := r.Decide("/team parallel analyze logs", ModeSingleAgent); got != ModeTeams {
		t.Errorf("explicit /team should force Teams, got %v", got)
	}
	if got := r.Decide("/deep implement feature", ModeSingleAgent); got != ModeDeepAgent {
		t.Errorf("explicit /deep should force DeepAgent, got %v", got)
	}
	if got := r.Decide("simple", ModeDeepAgent); got != ModeDeepAgent {
		t.Errorf("explicit DeepAgent should override auto, got %v", got)
	}
}

func TestOrchestratorRouterDescribe(t *testing.T) {
	r := NewRouter()
	if desc := r.Describe("x", ModeTeams); desc == "" {
		t.Error("Describe should return non-empty for Teams")
	}
	if desc := r.Describe("x", ModeDeepAgent); desc == "" {
		t.Error("Describe should return non-empty for DeepAgent")
	}
	if desc := r.Describe("x", ModeSingleAgent); desc == "" {
		t.Error("Describe should return non-empty for SingleAgent")
	}
}

func TestOrchestratorRouterCustomPrefix(t *testing.T) {
	r := NewRouter()
	r.WithTeamsPrefix("/parallelteam", "/parallel-agent")
	if got := r.DecideAuto("/parallelteam do x"); got != ModeTeams {
		t.Errorf("custom prefix /parallelteam should force Teams, got %v", got)
	}
}

func TestBlackboardWriteReadDelete(t *testing.T) {
	b := NewBlackboard()
	if b.Size() != 0 {
		t.Errorf("new blackboard size = %d, want 0", b.Size())
	}

	b.Write("file.path.config", "explore", "config.yaml")
	b.Write("function.found.init", "explore", "Init()")
	b.Write("error.found.timeout", "verify", "connection timed out")

	if b.Size() != 3 {
		t.Errorf("size = %d, want 3", b.Size())
	}
	v, ok := b.Read("file.path.config")
	if !ok || v != "config.yaml" {
		t.Errorf("read file.path.config = %v, ok=%v", v, ok)
	}
	if _, ok := b.Read("nonexistent"); ok {
		t.Error("nonexistent key should not exist")
	}

	b.Delete("error.found.timeout")
	if b.Size() != 2 {
		t.Errorf("size after delete = %d, want 2", b.Size())
	}
}

func TestBlackboardTTLExpiry(t *testing.T) {
	b := NewBlackboard()
	b.WriteWithTTL("temp", "agent", "value", 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, ok := b.Read("temp"); ok {
		t.Error("TTL entry should have expired")
	}
	snap := b.Snapshot()
	if _, ok := snap["temp"]; ok {
		t.Error("expired entry should not appear in Snapshot")
	}
}

func TestBlackboardSummary(t *testing.T) {
	b := NewBlackboard()
	if b.Summary() != "" {
		t.Error("empty Blackboard Summary should be empty")
	}
	b.Write("risk.1", "verify", "nil pointer at line 42")
	b.Write("file.1", "explore", "src/main.go")
	s := b.Summary()
	if s == "" {
		t.Error("Summary should be non-empty after writes")
	}
	if !containsSubstring(s, "Blackboard") {
		t.Error("Summary should include header")
	}
	if !containsSubstring(s, "risk.1") {
		t.Error("Summary should include risk.1")
	}
	if !containsSubstring(s, "file.1") {
		t.Error("Summary should include file.1")
	}
}

func TestBlackboardStructuredWrites(t *testing.T) {
	b := NewBlackboard()
	b.WriteFileRecord("agent-1", "/src/app.go")
	b.WriteFunctionRecord("agent-1", "HandleRequest", "app.go")
	b.WriteErrorRecord("agent-2", "nil pointer", "runtime error")
	b.WriteTestResult("agent-3", "TestFoo", true, 15)
	b.WriteTestResult("agent-3", "TestBar", false, 120)

	if b.Size() < 4 {
		t.Errorf("size = %d, want >= 4", b.Size())
	}
	s := b.Summary()
	if !containsSubstring(s, "TestFoo") || !containsSubstring(s, "pass") {
		t.Error("Summary should include pass result")
	}
	if !containsSubstring(s, "TestBar") || !containsSubstring(s, "fail") {
		t.Error("Summary should include fail result")
	}
}

func TestBlackboardClear(t *testing.T) {
	b := NewBlackboard()
	b.Write("a", "agent", 1)
	b.Write("b", "agent", 2)
	b.Clear()
	if b.Size() != 0 {
		t.Errorf("after clear size = %d, want 0", b.Size())
	}
}

// fake repo implementations for testing SessionForkService.
type fakeSessionRepo struct {
	data map[string]*sessmodel.Session
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{data: map[string]*sessmodel.Session{}}
}

func (r *fakeSessionRepo) Save(_ context.Context, s *sessmodel.Session) error {
	if s == nil {
		return nil
	}
	if r.data == nil {
		r.data = map[string]*sessmodel.Session{}
	}
	r.data[s.ID] = s
	return nil
}

func (r *fakeSessionRepo) FindByID(_ context.Context, id string) (*sessmodel.Session, error) {
	return r.data[id], nil
}

func (r *fakeSessionRepo) ListByUser(_ context.Context, _ string, _ int) ([]*sessmodel.Session, error) {
	return nil, nil
}

type fakeMessageRepo struct {
	data map[string][]*sessmodel.Message
}

func newFakeMessageRepo() *fakeMessageRepo {
	return &fakeMessageRepo{data: map[string][]*sessmodel.Message{}}
}

func (r *fakeMessageRepo) Save(_ context.Context, m *sessmodel.Message) error {
	if m == nil {
		return nil
	}
	if r.data == nil {
		r.data = map[string][]*sessmodel.Message{}
	}
	r.data[m.SessionID] = append(r.data[m.SessionID], m)
	return nil
}

func (r *fakeMessageRepo) ListBySession(_ context.Context, sessionID string, _ int) ([]*sessmodel.Message, error) {
	return r.data[sessionID], nil
}

func (r *fakeMessageRepo) ListAsMaps(_ context.Context, sessionID string, _ int) ([]map[string]any, error) {
	return nil, nil
}

// Ensure fake repos satisfy the repository interfaces.
var (
	_ sessrepo.ISessionRepository = (*fakeSessionRepo)(nil)
	_ sessrepo.IMessageRepository = (*fakeMessageRepo)(nil)
)

func TestSessionForkService(t *testing.T) {
	sessRepo := newFakeSessionRepo()
	msgRepo := newFakeMessageRepo()
	svc := NewSessionForkService(sessRepo, msgRepo)

	res, err := svc.ForkSession(context.Background(), ForkRequest{
		SourceSessionID: "src-1",
		SourceWorkspace: "/home/user/proj",
		NewWorkspace:    "/home/user/proj/worktree-1",
		SessionKind:     "worktree",
		SubagentRole:    "explore",
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if res.NewSessionID == "" {
		t.Error("NewSessionID should be set")
	}
	if res.ParentID != "src-1" {
		t.Errorf("ParentID = %s, want src-1", res.ParentID)
	}
	if res.NewWorkspace != "/home/user/proj/worktree-1" {
		t.Errorf("NewWorkspace = %s", res.NewWorkspace)
	}

	sess, err := svc.GetSession(context.Background(), res.NewSessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session should be retrievable")
	}
	if sess.WorkingDir != "/home/user/proj/worktree-1" {
		t.Errorf("WorkingDir = %s", sess.WorkingDir)
	}

	if err := svc.MergeSummary(context.Background(), "src-1", "Found config.yaml", res.NewSessionID, "explore"); err != nil {
		t.Fatalf("MergeSummary: %v", err)
	}
}

func TestSessionForkIDGeneration(t *testing.T) {
	id1 := generateForkID()
	id2 := generateForkID()
	if id1 == id2 {
		t.Error("two fork IDs should be unique")
	}
	if len(id1) < 5 || id1[:3] != "fk_" {
		t.Errorf("bad fork id: %s", id1)
	}
}

func TestForkContextDuration(t *testing.T) {
	fc := NewForkContext("run-1", "parent-1", "sub-1", "explore", "/tmp")
	if fc.DurationMs() < 0 {
		t.Error("DurationMs should be >= 0")
	}
	time.Sleep(15 * time.Millisecond)
	if fc.DurationMs() < 10 {
		t.Error("DurationMs should be >= 10ms after sleep")
	}
}

// helpers ----------------------------------------------------------------

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
