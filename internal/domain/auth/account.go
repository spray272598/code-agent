package auth

import "time"

// Role values for a user.
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

// User is a platform account.
type User struct {
	ID            string
	Email         string
	PasswordHash  string
	DisplayName   string
	Role          string
	Status        string
	EmailVerified bool
	VerifyToken   string
	// ResetToken is the one-time password reset token (empty when not pending).
	ResetToken    string
	ResetExpiresAt time.Time
	// QuotaTokens is the per-reset token budget; negative means unlimited.
	QuotaTokens int64
	QuotaResetAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Device is an RFC8628 device authorization record (the device_code is the primary key).
type Device struct {
	ID         string // device_code
	UserCode   string
	UserID     string
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
	DeviceID  string
	TokenHash string
	Scope     string
	ExpiresAt time.Time
	Revoked   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
