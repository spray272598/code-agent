package auth

import "context"

// UserRepository persists platform users.
type UserRepository interface {
	Save(ctx context.Context, u *User) error
	FindByID(ctx context.Context, id string) (*User, error)
	FindByEmail(ctx context.Context, orgID, email string) (*User, error)
	FindByVerifyToken(ctx context.Context, token string) (*User, error)
	FindByResetToken(ctx context.Context, token string) (*User, error)
	ListByOrg(ctx context.Context, orgID string) ([]*User, error)
}

// OrgRepository persists organizations (tenants).
type OrgRepository interface {
	Save(ctx context.Context, o *Organization) error
	FindByID(ctx context.Context, id string) (*Organization, error)
	FindBySlug(ctx context.Context, slug string) (*Organization, error)
}

// DeviceRepository persists RFC8628 device authorization records.
type DeviceRepository interface {
	Save(ctx context.Context, d *Device) error
	FindByDeviceCode(ctx context.Context, deviceCode string) (*Device, error)
	FindByUserCode(ctx context.Context, userCode string) (*Device, error)
}

// RefreshTokenRepository persists refresh token JTIs and supports revocation.
type RefreshTokenRepository interface {
	Save(ctx context.Context, t *RefreshToken) error
	FindByID(ctx context.Context, jid string) (*RefreshToken, error)
	FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	Revoke(ctx context.Context, jid string) error
	RevokeAllForUser(ctx context.Context, userID string) error
}
