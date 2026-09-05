package application

import (
	"context"
	"testing"

	sshmodel "github.com/spray272598/code-agent/internal/domain/ssh/model"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

// TestChatApp_TrySlash tests slash command handling.
func TestChatApp_TrySlash(t *testing.T) {
	app := &ChatApp{
		slash: nil, // no slash registry
	}

	req := &ChatRequest{Message: "/help"}
	resp, handled, fc := app.trySlash(req)

	if handled {
		t.Error("expected handled=false when slash is nil")
	}
	if fc {
		t.Error("expected forceCompact=false")
	}
	if resp != nil {
		t.Error("expected nil response")
	}
}

func TestChatApp_TrySlash_NoPrefix(t *testing.T) {
	app := &ChatApp{
		slash: nil,
	}

	req := &ChatRequest{Message: "hello world"}
	_, handled, _ := app.trySlash(req)

	if handled {
		t.Error("expected handled=false for non-slash message")
	}
}

// TestChatApp_Idempotency tests idempotency check.
func TestChatApp_Idempotency(t *testing.T) {
	app := &ChatApp{
		idemSvc: &IdempotencyService{},
		redis:   nil,
	}

	req := ChatRequest{
		UserID: "user-1",
	}

	// No redis - should return "none"
	status, cached, err := app.checkIdempotency(context.Background(), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status != "none" {
		t.Errorf("expected status 'none', got %s", status)
	}
	if cached != nil {
		t.Error("expected nil cached response")
	}
}

// TestChatApp_IdempotencyKey tests idempotency with key.
func TestChatApp_IdempotencyKey(t *testing.T) {
	app := &ChatApp{
		idemSvc: &IdempotencyService{},
		redis:   nil,
	}

	req := ChatRequest{
		UserID:         "user-1",
		IdempotencyKey: "idem-key-1",
	}

	// No redis store available - should return "none"
	status, _, err := app.checkIdempotency(context.Background(), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status != "none" {
		t.Errorf("expected status 'none', got %s", status)
	}
}

// TestChatApp_RateLimit tests rate limiting logic.
func TestChatApp_RateLimit(t *testing.T) {
	app := &ChatApp{
		rateSvc: &RateQuotaService{
			rateEnabled: false,
			ratePerMin:  60,
		},
	}

	// Rate disabled - should pass
	err := app.checkRate(context.Background(), "user-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestChatApp_RateLimit_Enabled tests rate limiting when enabled.
func TestChatApp_RateLimit_Enabled(t *testing.T) {
	app := &ChatApp{
		rateSvc: &RateQuotaService{
			rateEnabled: true,
			ratePerMin:  60,
			redis:       nil, // no redis
		},
	}

	// No redis - should pass
	err := app.checkRate(context.Background(), "user-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestChatApp_QuotaCheck tests quota checking logic.
func TestChatApp_QuotaCheck(t *testing.T) {
	app := &ChatApp{
		rateSvc: &RateQuotaService{
			quotaEnabled: false,
			quotaPerDay:  2000000,
		},
	}

	// Quota disabled - should pass
	err := app.checkQuota(context.Background(), "user-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestChatApp_QuotaCheck_Enabled tests quota check when enabled.
func TestChatApp_QuotaCheck_Enabled(t *testing.T) {
	app := &ChatApp{
		rateSvc: &RateQuotaService{
			quotaEnabled: true,
			quotaPerDay:  2000000,
			redis:        nil, // no redis
		},
	}

	// No redis - should pass
	err := app.checkQuota(context.Background(), "user-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestChatApp_ListTools tests tool listing with empty registry.
func TestChatApp_ListTools(t *testing.T) {
	app := &ChatApp{
		tools: &tool.MapRegistry{},
	}

	tools := app.ListTools()
	if tools == nil {
		t.Error("expected non-nil result from empty registry")
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

// TestChatApp_SetMethods tests setter methods.
func TestChatApp_SetMethods(t *testing.T) {
	app := &ChatApp{}

	// Test SetSkills
	app.SetSkills(nil)
	if app.skills != nil {
		t.Error("expected nil skills")
	}

	// Test SetMemory
	app.SetMemory(nil)
	if app.memSvc != nil {
		t.Error("expected nil memory service")
	}

	// Test SetAudit
	app.SetAudit(nil)
	if app.auditRepo != nil {
		t.Error("expected nil audit repo")
	}

	// Test SetBlobStore
	app.SetBlobStore(nil)
	if app.blobs != nil {
		t.Error("expected nil blob store")
	}
}

// TestChatApp_MCPDelegation tests MCP delegation methods.
func TestChatApp_MCPDelegation(t *testing.T) {
	app := &ChatApp{}

	// Test MCPFor without factory
	_, err := app.MCPFor(context.Background())
	if err == nil {
		t.Error("expected error when mcp factory not configured")
	}

	// Test MCPFactory getter
	if app.MCPFactory() != nil {
		t.Error("expected nil MCP factory")
	}
}

// TestChatApp_SSHDelegation tests SSH delegation methods.
func TestChatApp_SSHDelegation(t *testing.T) {
	app := &ChatApp{}

	// Test SSHPool without service
	if app.SSHPool() != nil {
		t.Error("expected nil SSH pool")
	}

	// Test InstallSSH without service
	err := app.InstallSSH(context.Background(), sshmodel.ConnectionConfig{})
	if err == nil {
		t.Error("expected error when SSH disabled")
	}

	// Test ListSSHConnections without service
	_, err = app.ListSSHConnections(context.Background())
	if err == nil {
		t.Error("expected error when SSH disabled")
	}

	// Test DeleteSSHConnection without service
	err = app.DeleteSSHConnection(context.Background(), "test")
	if err == nil {
		t.Error("expected error when SSH disabled")
	}
}

// TestChatApp_MemoryDelegation tests memory delegation methods.
func TestChatApp_MemoryDelegation(t *testing.T) {
	app := &ChatApp{
		memSvc: nil,
	}

	// Test SaveMemory with nil service
	err := app.SaveMemory(context.Background(), nil)
	if err == nil {
		t.Error("expected error with nil memory service")
	}

	// Test ListMemory with nil service
	_, err = app.ListMemory(context.Background(), "proj-1", "global", 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Test SearchMemory with nil service
	_, err = app.SearchMemory(context.Background(), "proj-1", "query", 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestChatApp_AuditDelegation tests audit delegation methods.
func TestChatApp_AuditDelegation(t *testing.T) {
	app := &ChatApp{
		auditRepo: nil,
	}

	// Test ListAudit with nil repo
	entries, err := app.ListAudit(context.Background(), "user-1", "session-1", 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Error("expected nil entries with nil repo")
	}

	// Test ListAuditCtx with nil repo
	entries, err = app.ListAuditCtx(context.Background(), "session-1", 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Error("expected nil entries with nil repo")
	}
}

// TestChatApp_BlobDelegation tests blob delegation methods.
func TestChatApp_BlobDelegation(t *testing.T) {
	app := &ChatApp{
		blobs: nil,
	}

	// Test GetBlob with nil store
	_, err := app.GetBlob(context.Background(), "key-1")
	if err == nil {
		t.Error("expected error with nil blob store")
	}

	// Test Blobs getter
	if app.Blobs() != nil {
		t.Error("expected nil blob store")
	}
}

// TestNewID tests ID generation.
func TestNewID(t *testing.T) {
	id1 := newID("test")
	id2 := newID("test")

	if id1 == "" {
		t.Error("expected non-empty ID")
	}
	if id1 == id2 {
		t.Error("expected unique IDs")
	}
}

// TestQuotaExceeded tests quota exceeded logic.
func TestQuotaExceeded(t *testing.T) {
	tests := []struct {
		used   int
		quota  int
		expect bool
	}{
		{0, 100, false},
		{99, 100, false},
		{100, 100, true},
		{101, 100, true},
		{100, 0, false},
		{100, -1, false},
	}

	for _, tt := range tests {
		got := quotaExceeded(tt.used, tt.quota)
		if got != tt.expect {
			t.Errorf("quotaExceeded(%d, %d) = %v, want %v", tt.used, tt.quota, got, tt.expect)
		}
	}
}

// TestIdemKey tests idempotency key generation.
func TestIdemKey(t *testing.T) {
	tests := []struct {
		userID string
		key    string
		want   string
	}{
		{"user-1", "key-1", "idem:user-1:key-1"},
		{"", "key-1", "idem:key-1"},
		{"user-1", "", "idem:user-1:"},
	}

	for _, tt := range tests {
		got := idemKey(tt.userID, tt.key)
		if got != tt.want {
			t.Errorf("idemKey(%q, %q) = %q, want %q", tt.userID, tt.key, got, tt.want)
		}
	}
}

// TestTodayKey tests date key generation.
func TestTodayKey(t *testing.T) {
	key := todayKey()
	if key == "" {
		t.Error("expected non-empty date key")
	}
	// Format should be YYYY-MM-DD
	if len(key) != 10 {
		t.Errorf("expected 10 characters, got %d", len(key))
	}
}
