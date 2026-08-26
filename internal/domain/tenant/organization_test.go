package tenant

import (
	"context"
	"testing"
)

func TestOrgManagerCreateAndGet(t *testing.T) {
	mgr := NewOrgManager()
	org, err := mgr.CreateOrg("org-1", "Test Org", "user-1")
	if err != nil {
		t.Fatalf("CreateOrg failed: %v", err)
	}
	if org.ID != "org-1" {
		t.Errorf("expected org ID org-1, got %s", org.ID)
	}
	if org.OwnerID != "user-1" {
		t.Errorf("expected owner user-1, got %s", org.OwnerID)
	}

	got, err := mgr.GetOrg("org-1")
	if err != nil {
		t.Fatalf("GetOrg failed: %v", err)
	}
	if got.Name != org.Name {
		t.Errorf("expected name %s, got %s", org.Name, got.Name)
	}
}

func TestOrgManagerDuplicateCreation(t *testing.T) {
	mgr := NewOrgManager()
	_, err := mgr.CreateOrg("org-1", "Test", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = mgr.CreateOrg("org-1", "Test2", "user-2")
	if err != ErrOrgExists {
		t.Errorf("expected ErrOrgExists, got %v", err)
	}
}

func TestOrgMemberManagement(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")

	member, err := mgr.AddMember("org-1", "user-1", RoleMember)
	if err != nil {
		t.Fatalf("AddMember failed: %v", err)
	}
	if member.Role != RoleMember {
		t.Errorf("expected RoleMember, got %s", member.Role)
	}

	_, err = mgr.AddMember("org-1", "user-1", RoleAdmin)
	if err != ErrMemberExists {
		t.Errorf("expected ErrMemberExists, got %v", err)
	}

	err = mgr.UpdateMemberRole("org-1", "user-1", RoleAdmin)
	if err != nil {
		t.Fatalf("UpdateMemberRole failed: %v", err)
	}

	updated, _ := mgr.GetMember("org-1", "user-1")
	if updated.Role != RoleAdmin {
		t.Errorf("expected RoleAdmin, got %s", updated.Role)
	}

	err = mgr.RemoveMember("org-1", "user-1")
	if err != nil {
		t.Fatalf("RemoveMember failed: %v", err)
	}

	_, err = mgr.GetMember("org-1", "user-1")
	if err != ErrMemberNotFound {
		t.Errorf("expected ErrMemberNotFound, got %v", err)
	}
}

func TestOrgCannotRemoveOwner(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	err := mgr.RemoveMember("org-1", "owner")
	if err != ErrCannotRemoveOwner {
		t.Errorf("expected ErrCannotRemoveOwner, got %v", err)
	}
}

func TestOrgWorkspaceManagement(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")

	ws, err := mgr.CreateWorkspace("org-1", "ws-1", "Workspace 1", "/path/to/ws", "owner")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if ws.Name != "Workspace 1" {
		t.Errorf("expected name 'Workspace 1', got '%s'", ws.Name)
	}

	got, err := mgr.GetWorkspace("org-1", "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RootPath != "/path/to/ws" {
		t.Errorf("expected root path, got %s", got.RootPath)
	}

	workspaces, err := mgr.ListWorkspaces("org-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(workspaces))
	}
}

func TestOrgWorkspaceAccessControl(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	mgr.CreateWorkspace("org-1", "ws-1", "WS", "/tmp", "owner")
	_, _ = mgr.AddMember("org-1", "member-1", RoleMember)
	_, _ = mgr.AddMember("org-1", "viewer-1", RoleViewer)

	ownerAccess, err := mgr.GetWorkspaceAccess("org-1", "ws-1", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !ownerAccess.CanRead || !ownerAccess.CanWrite || !ownerAccess.CanAdmin {
		t.Error("owner should have full access")
	}

	memberAccess, err := mgr.GetWorkspaceAccess("org-1", "ws-1", "member-1")
	if err != nil {
		t.Fatal(err)
	}
	if !memberAccess.CanRead || !memberAccess.CanWrite {
		t.Error("member should have read+write")
	}
	if memberAccess.CanDelete || memberAccess.CanAdmin {
		t.Error("member should not have delete/admin")
	}

	viewerAccess, err := mgr.GetWorkspaceAccess("org-1", "ws-1", "viewer-1")
	if err != nil {
		t.Fatal(err)
	}
	if !viewerAccess.CanRead {
		t.Error("viewer should have read access")
	}
	if viewerAccess.CanWrite {
		t.Error("viewer should not have write access")
	}
}

func TestOrgCustomAccess(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	mgr.CreateWorkspace("org-1", "ws-1", "WS", "/tmp", "owner")
	_, _ = mgr.AddMember("org-1", "custom-user", RoleMember)

	mgr.SetWorkspaceAccess("org-1", "ws-1", "custom-user", ReadOnly)

	access, _ := mgr.GetWorkspaceAccess("org-1", "ws-1", "custom-user")
	if access.CanWrite {
		t.Error("custom user should be read-only after override")
	}
	if !access.CanRead {
		t.Error("custom user should still read")
	}
}

func TestOrgCanAccess(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	mgr.CreateWorkspace("org-1", "ws-1", "WS", "/tmp", "owner")
	_, _ = mgr.AddMember("org-1", "member-1", RoleMember)

	if !mgr.CanAccess("org-1", "ws-1", "owner", "read") {
		t.Error("owner should have read access")
	}
	if !mgr.CanAccess("org-1", "ws-1", "owner", "admin") {
		t.Error("owner should have admin access")
	}
	if !mgr.CanAccess("org-1", "ws-1", "member-1", "write") {
		t.Error("member should have write access")
	}
	if mgr.CanAccess("org-1", "ws-1", "member-1", "admin") {
		t.Error("member should not have admin access")
	}
	if mgr.CanAccess("org-1", "ws-1", "nonexistent", "read") {
		t.Error("nonexistent user should not have any access")
	}
}

func TestOrgDelete(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	mgr.CreateWorkspace("org-1", "ws-1", "WS", "/tmp", "owner")

	err := mgr.DeleteOrg("org-1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = mgr.GetOrg("org-1")
	if err != ErrOrgNotFound {
		t.Errorf("expected ErrOrgNotFound, got %v", err)
	}
}

func TestOrgDeleteWorkspace(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	mgr.CreateWorkspace("org-1", "ws-1", "WS", "/tmp", "owner")
	_, _ = mgr.AddMember("org-1", "member-1", RoleMember)

	err := mgr.DeleteWorkspace("org-1", "ws-1", "member-1")
	if err != ErrPermissionDenied {
		t.Errorf("expected ErrPermissionDenied, got %v", err)
	}

	err = mgr.DeleteWorkspace("org-1", "ws-1", "owner")
	if err != nil {
		t.Fatalf("owner should be able to delete: %v", err)
	}

	_, err = mgr.GetWorkspace("org-1", "ws-1")
	if err != ErrWorkspaceNotFound {
		t.Errorf("expected ErrWorkspaceNotFound, got %v", err)
	}
}

func TestOrgContext(t *testing.T) {
	oc := OrgContext{
		TenantID:    "tenant-1",
		OrgID:       "org-1",
		WorkspaceID: "ws-1",
		UserRole:    RoleAdmin,
	}

	ctx := WithOrg(context.Background(), oc)
	got, ok := OrgFrom(ctx)
	if !ok {
		t.Fatal("expected OrgContext in ctx")
	}
	if got.OrgID != "org-1" {
		t.Errorf("expected org-1, got %s", got.OrgID)
	}
	if got.UserRole != RoleAdmin {
		t.Errorf("expected RoleAdmin, got %s", got.UserRole)
	}
}

func TestOrgRoles(t *testing.T) {
	if !RoleOwner.Can(RoleAdmin) {
		t.Error("owner should be able to do admin tasks")
	}
	if !RoleAdmin.Can(RoleMember) {
		t.Error("admin should be able to do member tasks")
	}
	if !RoleMember.Can(RoleViewer) {
		t.Error("member should be able to do viewer tasks")
	}
	if RoleViewer.Can(RoleMember) {
		t.Error("viewer should NOT be able to do member tasks")
	}
	if RoleViewer.Can(RoleOwner) {
		t.Error("viewer should NOT be able to do owner tasks")
	}
}

func TestOrgDefaultWorkspace(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	mgr.CreateWorkspace("org-1", "ws-1", "WS1", "/tmp/1", "owner")
	mgr.CreateWorkspace("org-1", "ws-2", "WS2", "/tmp/2", "owner")

	err := mgr.SetDefaultWorkspace("org-1", "ws-2")
	if err != nil {
		t.Fatal(err)
	}

	def, err := mgr.GetDefaultWorkspace("org-1")
	if err != nil {
		t.Fatal(err)
	}
	if def.ID != "ws-2" {
		t.Errorf("expected default ws-2, got %s", def.ID)
	}
}

func TestOrgListOrgs(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "First", "owner-1")
	_, _ = mgr.CreateOrg("org-2", "Second", "owner-2")

	orgs := mgr.ListOrgs()
	if len(orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d", len(orgs))
	}
}

func TestOrgMemberLimit(t *testing.T) {
	mgr := NewOrgManager()
	org, _ := mgr.CreateOrg("org-1", "Test", "owner")
	org.MaxMembers = 3

	_, _ = mgr.AddMember("org-1", "m1", RoleMember)
	_, _ = mgr.AddMember("org-1", "m2", RoleMember)

	_, err := mgr.AddMember("org-1", "m3", RoleMember)
	if err != ErrOrgMemberLimit {
		t.Errorf("expected ErrOrgMemberLimit, got %v", err)
	}
}

func TestOrgWorkspaceLimit(t *testing.T) {
	mgr := NewOrgManager()
	org, _ := mgr.CreateOrg("org-1", "Test", "owner")
	org.MaxWorkspaces = 2

	mgr.CreateWorkspace("org-1", "ws-1", "WS1", "/tmp/1", "owner")
	mgr.CreateWorkspace("org-1", "ws-2", "WS2", "/tmp/2", "owner")

	_, err := mgr.CreateWorkspace("org-1", "ws-3", "WS3", "/tmp/3", "owner")
	if err != ErrOrgWorkspaceLimit {
		t.Errorf("expected ErrOrgWorkspaceLimit, got %v", err)
	}
}

func TestOrgMemberPermissionForWorkspace(t *testing.T) {
	mgr := NewOrgManager()
	_, _ = mgr.CreateOrg("org-1", "Test", "owner")
	_, _ = mgr.AddMember("org-1", "viewer", RoleViewer)
	mgr.CreateWorkspace("org-1", "ws-1", "WS", "/tmp", "viewer")

	_, err := mgr.CreateWorkspace("org-1", "ws-2", "WS2", "/tmp/2", "viewer")
	if err != ErrPermissionDenied {
		t.Errorf("expected ErrPermissionDenied for viewer creating workspace, got %v", err)
	}
}
