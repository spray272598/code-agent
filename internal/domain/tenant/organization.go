package tenant

import (
	"context"
	"strings"
	"sync"
	"time"
)

// OrgRole defines the role of a user within an organization.
type OrgRole string

const (
	RoleOwner  OrgRole = "owner"
	RoleAdmin  OrgRole = "admin"
	RoleMember OrgRole = "member"
	RoleViewer OrgRole = "viewer"
)

// Can returns true if this role has the required permission level.
func (r OrgRole) Can(required OrgRole) bool {
	order := map[OrgRole]int{
		RoleOwner:  4,
		RoleAdmin:  3,
		RoleMember: 2,
		RoleViewer: 1,
	}
	return order[r] >= order[required]
}

func (r OrgRole) String() string { return string(r) }

// WorkspaceAccess defines what operations are allowed on a workspace.
type WorkspaceAccess struct {
	CanRead   bool
	CanWrite  bool
	CanDelete bool
	CanAdmin  bool
}

var (
	// FullAccess grants all permissions.
	FullAccess = WorkspaceAccess{CanRead: true, CanWrite: true, CanDelete: true, CanAdmin: true}
	// ReadOnly grants read-only access.
	ReadOnly = WorkspaceAccess{CanRead: true, CanWrite: false, CanDelete: false, CanAdmin: false}
	// WriteAccess grants read + write but not delete/admin.
	WriteAccess = WorkspaceAccess{CanRead: true, CanWrite: true, CanDelete: false, CanAdmin: false}
	// NoAccess grants no permissions.
	NoAccess = WorkspaceAccess{}
)

// Member represents a user's membership in an organization.
type Member struct {
	UserID       string    `json:"userId"`
	OrgID        string    `json:"orgId"`
	Role         OrgRole   `json:"role"`
	JoinedAt     time.Time `json:"joinedAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`
}

// Org represents an organization (tenant) with workspaces.
type Org struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	OwnerID       string    `json:"ownerId"`
	CreatedAt     time.Time `json:"createdAt"`
	MaxWorkspaces int       `json:"maxWorkspaces"`
	MaxMembers    int       `json:"maxMembers"`
}

// Workspace represents a scoped workspace within an organization.
type Workspace struct {
	ID        string            `json:"id"`
	OrgID     string            `json:"orgId"`
	Name      string            `json:"name"`
	RootPath  string            `json:"rootPath"`
	IsDefault bool              `json:"isDefault"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

// OrgManager manages organizations, members, and workspace access.
type OrgManager struct {
	mu         sync.RWMutex
	orgs       map[string]*Org
	members    map[string]map[string]*Member         // orgID -> userID -> Member
	workspaces map[string]map[string]*Workspace      // orgID -> workspaceID -> Workspace
	access     map[string]map[string]WorkspaceAccess // orgID:workspaceID -> userID -> Access
}

// NewOrgManager creates a new OrgManager.
func NewOrgManager() *OrgManager {
	return &OrgManager{
		orgs:       make(map[string]*Org),
		members:    make(map[string]map[string]*Member),
		workspaces: make(map[string]map[string]*Workspace),
		access:     make(map[string]map[string]WorkspaceAccess),
	}
}

// CreateOrg creates a new organization with the given owner.
func (m *OrgManager) CreateOrg(id, name, ownerID string) (*Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.orgs[id]; exists {
		return nil, ErrOrgExists
	}

	now := time.Now()
	org := &Org{
		ID:            id,
		Name:          name,
		OwnerID:       ownerID,
		CreatedAt:     now,
		MaxWorkspaces: 50,
		MaxMembers:    100,
	}

	m.orgs[id] = org
	members := make(map[string]*Member)
	members[ownerID] = &Member{
		UserID:   ownerID,
		OrgID:    id,
		Role:     RoleOwner,
		JoinedAt: now,
	}
	m.members[id] = members
	m.workspaces[id] = make(map[string]*Workspace)

	return org, nil
}

// GetOrg returns an organization by ID.
func (m *OrgManager) GetOrg(id string) (*Org, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	org, ok := m.orgs[id]
	if !ok {
		return nil, ErrOrgNotFound
	}
	return org, nil
}

// ListOrgs returns all organizations.
func (m *OrgManager) ListOrgs() []*Org {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Org, 0, len(m.orgs))
	for _, org := range m.orgs {
		result = append(result, org)
	}
	return result
}

// DeleteOrg removes an organization and all its data.
func (m *OrgManager) DeleteOrg(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.orgs[id]; !exists {
		return ErrOrgNotFound
	}
	delete(m.orgs, id)
	delete(m.members, id)
	delete(m.workspaces, id)
	for key := range m.access {
		if strings.HasPrefix(key, id+":") {
			delete(m.access, key)
		}
	}
	return nil
}

// AddMember adds a user to an organization.
func (m *OrgManager) AddMember(orgID, userID string, role OrgRole) (*Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	org, ok := m.orgs[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}

	members, ok := m.members[orgID]
	if !ok {
		members = make(map[string]*Member)
		m.members[orgID] = members
	}

	if _, exists := members[userID]; exists {
		return nil, ErrMemberExists
	}

	if len(members) >= org.MaxMembers {
		return nil, ErrOrgMemberLimit
	}

	member := &Member{
		UserID:   userID,
		OrgID:    orgID,
		Role:     role,
		JoinedAt: time.Now(),
	}
	members[userID] = member
	return member, nil
}

// RemoveMember removes a user from an organization.
func (m *OrgManager) RemoveMember(orgID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	members, ok := m.members[orgID]
	if !ok {
		return ErrOrgNotFound
	}
	member, ok := members[userID]
	if !ok {
		return ErrMemberNotFound
	}
	if member.Role == RoleOwner {
		return ErrCannotRemoveOwner
	}
	delete(members, userID)
	return nil
}

// UpdateMemberRole updates a member's role.
func (m *OrgManager) UpdateMemberRole(orgID, userID string, newRole OrgRole) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	members, ok := m.members[orgID]
	if !ok {
		return ErrOrgNotFound
	}
	member, ok := members[userID]
	if !ok {
		return ErrMemberNotFound
	}
	member.Role = newRole
	return nil
}

// ListMembers returns all members of an organization.
func (m *OrgManager) ListMembers(orgID string) ([]*Member, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	members, ok := m.members[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}
	result := make([]*Member, 0, len(members))
	for _, member := range members {
		result = append(result, member)
	}
	return result, nil
}

// GetMember returns a specific member.
func (m *OrgManager) GetMember(orgID, userID string) (*Member, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	members, ok := m.members[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}
	member, ok := members[userID]
	if !ok {
		return nil, ErrMemberNotFound
	}
	return member, nil
}

// CreateWorkspace creates a workspace within an organization.
func (m *OrgManager) CreateWorkspace(orgID, wsID, name, rootPath string, creatorID string) (*Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	org, ok := m.orgs[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}

	members, ok := m.members[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}
	creator, ok := members[creatorID]
	if !ok {
		return nil, ErrMemberNotFound
	}
	if !creator.Role.Can(RoleAdmin) {
		return nil, ErrPermissionDenied
	}

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		wsMap = make(map[string]*Workspace)
		m.workspaces[orgID] = wsMap
	}

	if len(wsMap) >= org.MaxWorkspaces {
		return nil, ErrOrgWorkspaceLimit
	}

	ws := &Workspace{
		ID:        wsID,
		OrgID:     orgID,
		Name:      name,
		RootPath:  rootPath,
		CreatedAt: time.Now(),
	}
	wsMap[wsID] = ws

	accessKey := orgID + ":" + wsID
	m.access[accessKey] = make(map[string]WorkspaceAccess)
	m.access[accessKey][creatorID] = FullAccess

	return ws, nil
}

// GetWorkspace returns a workspace by ID.
func (m *OrgManager) GetWorkspace(orgID, wsID string) (*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	ws, ok := wsMap[wsID]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	return ws, nil
}

// ListWorkspaces lists workspaces in an organization.
func (m *OrgManager) ListWorkspaces(orgID string) ([]*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}
	result := make([]*Workspace, 0, len(wsMap))
	for _, ws := range wsMap {
		result = append(result, ws)
	}
	return result, nil
}

// DeleteWorkspace removes a workspace.
func (m *OrgManager) DeleteWorkspace(orgID, wsID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		return ErrWorkspaceNotFound
	}
	ws, ok := wsMap[wsID]
	if !ok {
		return ErrWorkspaceNotFound
	}

	members, ok := m.members[orgID]
	if !ok {
		return ErrOrgNotFound
	}
	member, ok := members[userID]
	if !ok {
		return ErrMemberNotFound
	}
	if !member.Role.Can(RoleAdmin) {
		return ErrPermissionDenied
	}

	delete(wsMap, wsID)
	delete(m.access, orgID+":"+wsID)

	if ws.IsDefault {
		for _, w := range wsMap {
			w.IsDefault = true
			break
		}
	}

	return nil
}

// SetWorkspaceAccess sets access permissions for a user on a workspace.
func (m *OrgManager) SetWorkspaceAccess(orgID, wsID, userID string, access WorkspaceAccess) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	accessKey := orgID + ":" + wsID
	accessMap, ok := m.access[accessKey]
	if !ok {
		accessMap = make(map[string]WorkspaceAccess)
		m.access[accessKey] = accessMap
	}
	accessMap[userID] = access
	return nil
}

// GetWorkspaceAccess returns a user's access to a workspace.
func (m *OrgManager) GetWorkspaceAccess(orgID, wsID, userID string) (WorkspaceAccess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	accessKey := orgID + ":" + wsID
	accessMap, ok := m.access[accessKey]
	if !ok {
		return NoAccess, nil
	}

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		return NoAccess, nil
	}
	_, ok = wsMap[wsID]
	if !ok {
		return NoAccess, ErrWorkspaceNotFound
	}

	if acc, ok := accessMap[userID]; ok {
		return acc, nil
	}

	members, ok := m.members[orgID]
	if !ok {
		return NoAccess, nil
	}
	member, ok := members[userID]
	if !ok {
		return NoAccess, nil
	}

	switch member.Role {
	case RoleOwner:
		return FullAccess, nil
	case RoleAdmin:
		return FullAccess, nil
	case RoleMember:
		return WriteAccess, nil
	case RoleViewer:
		return ReadOnly, nil
	default:
		return NoAccess, nil
	}
}

// CanAccess checks if a user has the required access level on a workspace.
func (m *OrgManager) CanAccess(orgID, wsID, userID string, require string) bool {
	access, err := m.GetWorkspaceAccess(orgID, wsID, userID)
	if err != nil {
		return false
	}
	switch require {
	case "read":
		return access.CanRead
	case "write":
		return access.CanRead && access.CanWrite
	case "delete":
		return access.CanDelete
	case "admin":
		return access.CanAdmin
	default:
		return false
	}
}

// SetDefaultWorkspace sets a workspace as the default for an organization.
func (m *OrgManager) SetDefaultWorkspace(orgID, wsID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		return ErrOrgNotFound
	}
	for id, ws := range wsMap {
		ws.IsDefault = (id == wsID)
	}
	return nil
}

// GetDefaultWorkspace returns the default workspace for an organization.
func (m *OrgManager) GetDefaultWorkspace(orgID string) (*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wsMap, ok := m.workspaces[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}
	for _, ws := range wsMap {
		if ws.IsDefault {
			return ws, nil
		}
	}
	for _, ws := range wsMap {
		return ws, nil
	}
	return nil, ErrWorkspaceNotFound
}

// UpdateMemberActivity updates the last active time for a member.
func (m *OrgManager) UpdateMemberActivity(orgID, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	members, ok := m.members[orgID]
	if !ok {
		return
	}
	member, ok := members[userID]
	if !ok {
		return
	}
	member.LastActiveAt = time.Now()
}

// OrgContext holds the multi-tenant context for a request.
type OrgContext struct {
	TenantID    string
	OrgID       string
	WorkspaceID string
	UserRole    OrgRole
}

type orgCtxKey struct{}

// WithOrg stores the OrgContext on ctx.
func WithOrg(ctx context.Context, oc OrgContext) context.Context {
	return context.WithValue(ctx, orgCtxKey{}, oc)
}

// OrgFrom retrieves the OrgContext from ctx.
func OrgFrom(ctx context.Context) (OrgContext, bool) {
	oc, ok := ctx.Value(orgCtxKey{}).(OrgContext)
	return oc, ok
}
