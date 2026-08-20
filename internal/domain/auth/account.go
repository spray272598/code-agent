package auth

import "time"

// Role values for a user inside an organization.
const (
	RoleOwner   = "owner"
	RoleAdmin   = "admin"
	RoleMember  = "member"
	RoleViewer  = "viewer"
)

// Status of a user account lifecycle.
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusDeleted   = "deleted"
)

// Status of an RFC8628 device authorization request.
const (
	DevicePending  = "pending"
	DeviceApproved = "approved"
	DeviceDenied   = "denied"
	DeviceExpired  = "expired"
)

// Subscription plan of an organization.
const (
	PlanFree        = "free"
	PlanPro         = "pro"
	PlanEnterprise  = "enterprise"
)

// User is a platform account belonging to exactly one organization (tenant).
type User struct {
	ID            string
	OrgID         string
	Email         string
	PasswordHash  string
	DisplayName   string
	Role          string
	Status        string
	EmailVerified bool
	VerifyToken   string
	// QuotaTokens is the per-reset token budget; negative means unlimited.
	QuotaTokens int64
	QuotaResetAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Organization is the tenant root. Every user, memory and session is scoped to it.
type Organization struct {
	ID        string
	Name      string
	Slug      string
	Plan      string
	OwnerID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Device is an RFC8628 device authorization record (the device_code is the primary key).
type Device struct {
	ID         string // device_code
	UserCode   string
	UserID     string
	OrgID      string
	Status     string
	Scope      string
	ExpiresAt  time.Time
	LastPollAt time.Time
	ApprovedAt time.Time
	CreatedAt  time.Time
}

// RefreshToken is a server-side refresh token record keyed by its JTI (id).
// TokenHash holds the SHA-256 of the opaque refresh token (never the raw token).
type RefreshToken struct {
	ID        string
	UserID    string
	OrgID     string
	DeviceID  string
	TokenHash string
	Scope     string
	ExpiresAt time.Time
	Revoked   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
