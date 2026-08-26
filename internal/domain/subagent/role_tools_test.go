package subagent

import "testing"

func TestIsSafeToolName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"read_file", true},
		{"grep", true},
		{"bash", true},
		{"_tool", true},
		{"a", false},           // too short (need 3+)
		{"", false},            // empty
		{"ReadFile", false},    // uppercase
		{"tool-name", false},   // hyphen not allowed in strict mode
		{"tool.name", false},   // dot not allowed in strict mode
		{"tool name", false},   // space not allowed
		{"tool;rm", false},     // semicolon not allowed
		{"tool;drop", false},   // injection attempt
		{"select*from", false}, // SQL injection
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSafeToolName(tt.name)
			if got != tt.want {
				t.Errorf("isSafeToolName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestFirstSafeCandidate(t *testing.T) {
	tests := []struct {
		name        string
		candidates  []string
		def         string
		wantResult  string
	}{
		{
			name:       "picks first safe candidate",
			candidates: []string{"unsafe;name", "safe_tool", "another"},
			def:        "default_tool",
			wantResult: "safe_tool",
		},
		{
			name:       "falls back to default when all unsafe",
			candidates: []string{"unsafe;name", "bad name"},
			def:        "default_tool",
			wantResult: "default_tool",
		},
		{
			name:       "handles empty candidates",
			candidates: []string{},
			def:        "default_tool",
			wantResult: "default_tool",
		},
		{
			name:       "handles empty string in candidates",
			candidates: []string{"", ""},
			def:        "default_tool",
			wantResult: "default_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstSafeCandidate(tt.candidates, tt.def)
			if got != tt.wantResult {
				t.Errorf("firstSafeCandidate() = %q, want %q", got, tt.wantResult)
			}
		})
	}
}

func TestRoleCapabilityIsSatisfied(t *testing.T) {
	tests := []struct {
		name    string
		cap     RoleCapability
		read    string
		search  string
		execute string
		want    bool
	}{
		{"none satisfied even with empty", RoleCapabilityNone, "", "", "", true},
		{"none satisfied with tools", RoleCapabilityNone, "read", "grep", "bash", true},
		{"skeptic satisfied", RoleCapabilitySkeptic, "read", "grep", "", true},
		{"skeptic missing read", RoleCapabilitySkeptic, "", "grep", "", false},
		{"skeptic missing search", RoleCapabilitySkeptic, "read", "", "", false},
		{"strategist satisfied", RoleCapabilityStrategist, "read", "grep", "bash", true},
		{"strategist missing execute", RoleCapabilityStrategist, "read", "grep", "", false},
		{"strategist missing read", RoleCapabilityStrategist, "", "grep", "bash", false},
		{"strategist missing search", RoleCapabilityStrategist, "read", "", "bash", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cap.IsSatisfied(tt.read, tt.search, tt.execute)
			if got != tt.want {
				t.Errorf("IsSatisfied() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoleToolNamesIsSafe(t *testing.T) {
	tests := []struct {
		name string
		tn   *RoleToolNames
		cap  RoleCapability
		want bool
	}{
		{
			name: "defaults are safe for skeptic",
			tn:   DefaultToolNames(),
			cap:  RoleCapabilitySkeptic,
			want: true,
		},
		{
			name: "defaults are safe for strategist",
			tn:   DefaultToolNames(),
			cap:  RoleCapabilityStrategist,
			want: true,
		},
		{
			name: "unsafe tool name fails strict safety check",
			tn: &RoleToolNames{
				Read: "safe_read", List: "safe_list", Search: "safe_search",
				Write: "safe_write", Edit: "safe_edit",
				Execute: "execute;rm", WebSearch: "safe_web", WebFetch: "safe_fetch",
			},
			cap:  RoleCapabilityStrategist,
			want: false,
		},
		{
			name: "missing required tool fails capability",
			tn: &RoleToolNames{
				Read: "", List: "list_dir", Search: "",
				Write: "write_file", Edit: "edit_file",
				Execute: "bash", WebSearch: "web_search", WebFetch: "web_fetch",
			},
			cap:  RoleCapabilitySkeptic,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tn.IsSafe(tt.cap)
			if got != tt.want {
				t.Errorf("IsSafe(%v) = %v, want %v", tt.cap, got, tt.want)
			}
		})
	}
}

func TestRoleToolNamesFromInherit(t *testing.T) {
	inherit := NewRoleToolNames("parent_read", "parent_list", "parent_search", "", "", "", "", "")
	child := &RoleToolNames{}
	child.FromInherit(inherit)

	if child.Read != "parent_read" {
		t.Errorf("expected parent_read, got %s", child.Read)
	}
	if child.List != "parent_list" {
		t.Errorf("expected parent_list, got %s", child.List)
	}
	if child.Search != "parent_search" {
		t.Errorf("expected parent_search, got %s", child.Search)
	}
	// These should get defaults since inherit doesn't specify them
	if child.Write != defaultWrite {
		t.Errorf("expected %s, got %s", defaultWrite, child.Write)
	}
	if child.Execute != defaultExecute {
		t.Errorf("expected %s, got %s", defaultExecute, child.Execute)
	}
}

func TestRoleToolNamesFromInherit_NilParent(t *testing.T) {
	child := &RoleToolNames{Read: "custom_read"}
	result := child.FromInherit(nil)

	if result.Read != "custom_read" {
		t.Errorf("expected custom_read, got %s", result.Read)
	}
}

func TestRoleToolNamesClone(t *testing.T) {
	original := DefaultToolNames()
	clone := original.Clone()

	if clone.Read != original.Read {
		t.Errorf("clone should have same Read: %q vs %q", clone.Read, original.Read)
	}

	// Modify clone, original should not change
	clone.Read = "modified"
	if original.Read == "modified" {
		t.Error("modifying clone should not affect original")
	}
}

func TestDefaultToolNamesWithCap(t *testing.T) {
	tn := DefaultToolNamesWithCap(RoleCapabilitySkeptic)
	if tn == nil {
		t.Fatal("expected non-nil tool names")
	}
	if !tn.IsSafe(RoleCapabilitySkeptic) {
		t.Error("defaults should satisfy skeptic capability")
	}

	tn2 := DefaultToolNamesWithCap(RoleCapabilityStrategist)
	if !tn2.IsSafe(RoleCapabilityStrategist) {
		t.Error("defaults should satisfy strategist capability")
	}
}