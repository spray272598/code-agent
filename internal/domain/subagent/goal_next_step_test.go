package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

// newSymlink creates linkPath -> target and returns the link path.
//
// It skips the test when the platform cannot produce a real symlink. Some
// Windows configurations (no developer mode, no SeCreateSymbolicLinkPrivilege,
// or a filtering filesystem layer) make os.Symlink return nil while writing a
// plain empty file instead of a reparse point. Asserting against that would
// fail for the wrong reason, so verify the link actually materialised.
func newSymlink(t *testing.T, target, linkPath string) string {
	t.Helper()
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("cannot create symlink (needs developer mode or admin on Windows): %v", err)
		return ""
	}
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Skipf("cannot stat created symlink: %v", err)
		return ""
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Skipf("platform did not create a real symlink at %s (mode %v); "+
			"symlink rejection cannot be exercised here", linkPath, info.Mode())
		return ""
	}
	return linkPath
}

func TestFirstUncheckedPlanItem(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "single unchecked item",
			content: "- [ ] Implement feature X\n",
			want:    "Implement feature X",
		},
		{
			name:    "mixed checked and unchecked",
			content: "- [x] Step 1 done\n- [ ] Step 2 pending\n- [x] Step 3 done\n",
			want:    "Step 2 pending",
		},
		{
			name:    "all checked",
			content: "- [x] Step 1\n- [x] Step 2\n",
			want:    "",
		},
		{
			name:    "empty checklist",
			content: "No items here\n",
			want:    "",
		},
		{
			name:    "indented checkbox",
			content: "  - [ ] Nested task\n",
			want:    "Nested task",
		},
		{
			name:    "checkbox with leading spaces",
			content: "   - [ ] Spaced task\n",
			want:    "Spaced task",
		},
		{
			name: "realistic plan",
			content: `# Implementation Plan

## Tasks
- [x] Set up project structure
- [x] Implement core API
- [ ] Add authentication layer
- [ ] Write integration tests
- [ ] Deploy to staging`,
			want: "Add authentication layer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstUncheckedPlanItem(tt.content)
			if got != tt.want {
				t.Errorf("firstUncheckedPlanItem() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveGoalNextStep(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")

	content := "# Plan\n- [x] Research phase complete\n- [ ] Implement API endpoints\n- [ ] Add tests"

	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveGoalNextStep(planPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Implement API endpoints" {
		t.Errorf("ResolveGoalNextStep() = %q, want %q", got, "Implement API endpoints")
	}
}

func TestResolveGoalNextStep_EmptyPath(t *testing.T) {
	got, err := ResolveGoalNextStep("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestResolveGoalNextStep_NonExistentFile(t *testing.T) {
	got, err := ResolveGoalNextStep("/nonexistent/path.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for non-existent file, got %q", got)
	}
}

func TestResolveGoalNextStep_AllChecked(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "done_plan.md")

	content := "# Plan\n- [x] Done\n"
	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveGoalNextStep(planPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string when all checked, got %q", got)
	}
}

func TestResolveGoalNextStep_LongItem(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "long_plan.md")

	longText := "This is a very long task description that goes on and on about implementing the highly complex distributed caching layer with Redis cluster support, including replication, failover, and client-side caching strategies for optimal performance in high-throughput production environments"
	content := "# Plan\n- [ ] " + longText + "\n"
	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveGoalNextStep(planPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len([]rune(got)) > GoalNextStepMaxChars+10 {
		t.Errorf("item too long: %d runes (max %d)", len([]rune(got)), GoalNextStepMaxChars)
	}
}

func TestResolveGoalNextStep_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real_plan.md")
	linkPath := filepath.Join(dir, "link_plan.md")

	if err := os.WriteFile(realPath, []byte("- [ ] Task\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	newSymlink(t, realPath, linkPath)

	if _, err := ResolveGoalNextStep(linkPath); err == nil {
		t.Error("expected error for symlink")
	}
}

func TestNeutralizeReminderTags(t *testing.T) {
	const zws = "\u200b"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no tags",
			input: "Normal text without tags",
			want:  "Normal text without tags",
		},
		{
			name:  "opening tag",
			input: "Inject <system-rem> here",
			want:  "Inject <system-rem" + zws + "> here",
		},
		{
			name:  "closing tag",
			input: "Close </system-rem> frame",
			want:  "Close </system-rem" + zws + "> frame",
		},
		{
			name:  "both tags",
			input: "<system-rem>evil</system-rem>",
			want:  "<system-rem" + zws + ">evil</system-rem" + zws + ">",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := neutralizeReminderTags(tt.input)
			if got != tt.want {
				t.Errorf("neutralizeReminderTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCapChars(t *testing.T) {
	long := "abcdefghij"
	result := CapChars(long, 5)
	if result != "abcde..." {
		t.Errorf("CapChars(%q, 5) = %q", long, result)
	}

	short := "abc"
	result = CapChars(short, 10)
	if result != "abc" {
		t.Errorf("CapChars(%q, 10) = %q", short, result)
	}
}

func TestPlanBaseline(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "baseline_plan.md")

	content := "# Plan\n- [ ] Task A\n"
	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	baseline, err := CapturePlanBaseline(planPath)
	if err != nil {
		t.Fatalf("CapturePlanBaseline: %v", err)
	}
	if baseline.ContentHash == "" {
		t.Error("expected non-empty hash")
	}

	ok, err := VerifyPlanIntegrity(baseline)
	if err != nil {
		t.Fatalf("VerifyPlanIntegrity: %v", err)
	}
	if !ok {
		t.Error("expected integrity check to pass")
	}

	if err := os.WriteFile(planPath, []byte("# Modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ok, err = VerifyPlanIntegrity(baseline)
	if err != nil {
		t.Fatalf("VerifyPlanIntegrity: %v", err)
	}
	if ok {
		t.Error("expected integrity check to fail after modification")
	}

	if err := RestorePlanFromBaseline(baseline); err != nil {
		t.Fatalf("RestorePlanFromBaseline: %v", err)
	}

	ok, err = VerifyPlanIntegrity(baseline)
	if err != nil {
		t.Fatalf("VerifyPlanIntegrity after restore: %v", err)
	}
	if !ok {
		t.Error("expected integrity check to pass after restore")
	}
}

func TestSymlinkCheck(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.txt")
	linkPath := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(realPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SymlinkCheck(realPath); err != nil {
		t.Errorf("SymlinkCheck on regular file should pass: %v", err)
	}

	newSymlink(t, realPath, linkPath)

	if err := SymlinkCheck(linkPath); err == nil {
		t.Error("SymlinkCheck should reject symlinks")
	}

	if err := SymlinkCheck(filepath.Join(dir, "nonexistent")); err == nil {
		t.Error("SymlinkCheck should error on non-existent file")
	}
}

func TestStrategistSnapshotManager(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "snapshot_plan.md")

	content := "# Plan v1\n- [ ] Task A\n"
	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := NewStrategistSnapshotManager(5)

	t.Run("create snapshot", func(t *testing.T) {
		entry, err := mgr.CreateSnapshot(planPath, "initial plan")
		if err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		if entry.Version != 1 {
			t.Errorf("expected version 1, got %d", entry.Version)
		}
		if entry.Hash == "" {
			t.Error("expected non-empty hash")
		}
		if entry.Reason != "initial plan" {
			t.Errorf("expected reason 'initial plan', got %q", entry.Reason)
		}
	})

	t.Run("create second snapshot", func(t *testing.T) {
		modified := "# Plan v2\n- [x] Task A\n- [ ] Task B\n"
		if err := os.WriteFile(planPath, []byte(modified), 0o600); err != nil {
			t.Fatal(err)
		}

		entry, err := mgr.CreateSnapshot(planPath, "after implementation")
		if err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		if entry.Version != 2 {
			t.Errorf("expected version 2, got %d", entry.Version)
		}
	})

	t.Run("verify integrity - match", func(t *testing.T) {
		// Should match version 2 (current content)
		matched, err := mgr.VerifyIntegrity(planPath)
		if err != nil {
			t.Fatalf("VerifyIntegrity: %v", err)
		}
		if matched == nil {
			t.Error("expected to find matching snapshot")
		}
		if matched.Version != 2 {
			t.Errorf("expected version 2 match, got %d", matched.Version)
		}
	})

	t.Run("verify integrity - mismatch", func(t *testing.T) {
		// Modify the file without creating a snapshot
		if err := os.WriteFile(planPath, []byte("# Unauthorized modification\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		matched, err := mgr.VerifyIntegrity(planPath)
		if err != nil {
			t.Fatalf("VerifyIntegrity: %v", err)
		}
		if matched != nil {
			t.Error("expected no matching snapshot after unauthorized modification")
		}
	})

	t.Run("restore snapshot", func(t *testing.T) {
		if err := mgr.RestoreSnapshot(planPath, 1); err != nil {
			t.Fatalf("RestoreSnapshot: %v", err)
		}

		// Read back and verify
		data, err := os.ReadFile(planPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "# Plan v1\n- [ ] Task A\n" {
			t.Errorf("restored content mismatch: %q", string(data))
		}

		// Verify integrity should now match
		matched, err := mgr.VerifyIntegrity(planPath)
		if err != nil {
			t.Fatalf("VerifyIntegrity after restore: %v", err)
		}
		if matched == nil {
			t.Error("expected to find matching snapshot after restore")
		}
	})

	t.Run("get history", func(t *testing.T) {
		history, err := mgr.GetHistory(planPath)
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		if len(history) < 2 {
			t.Errorf("expected at least 2 snapshots in history, got %d", len(history))
		}
	})

	t.Run("restore non-existent version", func(t *testing.T) {
		err := mgr.RestoreSnapshot(planPath, 999)
		if err == nil {
			t.Error("expected error for non-existent version")
		}
	})

	t.Run("clear history", func(t *testing.T) {
		mgr.ClearHistory(planPath)
		history, err := mgr.GetHistory(planPath)
		if err != nil {
			t.Fatalf("GetHistory after clear: %v", err)
		}
		if len(history) != 0 {
			t.Errorf("expected empty history after clear, got %d", len(history))
		}
	})

	t.Run("max history enforcement", func(t *testing.T) {
		content := "# Plan\n- [ ] Task\n"
		if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		smallMgr := NewStrategistSnapshotManager(3)
		for i := 0; i < 5; i++ {
			if _, err := smallMgr.CreateSnapshot(planPath, "test"); err != nil {
				t.Fatalf("CreateSnapshot %d: %v", i, err)
			}
		}

		history, err := smallMgr.GetHistory(planPath)
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		if len(history) > 3 {
			t.Errorf("history should be capped at 3, got %d", len(history))
		}
	})
}
