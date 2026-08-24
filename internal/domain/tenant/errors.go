package tenant

import "errors"

var (
	ErrOrgNotFound         = errors.New("organization not found")
	ErrOrgExists           = errors.New("organization already exists")
	ErrOrgMemberLimit      = errors.New("organization member limit reached")
	ErrOrgWorkspaceLimit   = errors.New("organization workspace limit reached")
	ErrMemberNotFound      = errors.New("member not found")
	ErrMemberExists        = errors.New("member already exists in this organization")
	ErrCannotRemoveOwner   = errors.New("cannot remove organization owner")
	ErrWorkspaceNotFound  = errors.New("workspace not found")
	ErrWorkspaceExists     = errors.New("workspace already exists")
	ErrPermissionDenied    = errors.New("permission denied")
	ErrInvalidRole         = errors.New("invalid role")
)