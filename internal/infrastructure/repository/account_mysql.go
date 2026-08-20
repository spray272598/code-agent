package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
)

// scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type scanner interface {
	Scan(dest ...any) error
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// quotaVal maps a quota value to a DB arg; negative means unlimited → NULL.
func quotaVal(n int64) any {
	if n < 0 {
		return nil
	}
	return n
}

// nullTime returns nil for the zero time so nullable columns stay NULL.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// nowOr returns t unless zero, in which case it returns the current time.
func nowOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

// ---- User (MySQL) ----

type MySQLUserRepo struct{ db *sql.DB }

func NewMySQLUserRepo(db *sql.DB) *MySQLUserRepo { return &MySQLUserRepo{db: db} }

var _ auth.UserRepository = (*MySQLUserRepo)(nil)

const userCols = `SELECT id,org_id,email,password_hash,display_name,role,status,email_verified,verify_token,quota_tokens,quota_reset_at,created_at,updated_at FROM users`

func (r *MySQLUserRepo) Save(ctx context.Context, u *auth.User) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO users (id,org_id,email,password_hash,display_name,role,status,email_verified,verify_token,quota_tokens,quota_reset_at,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE
  org_id=VALUES(org_id), email=VALUES(email), password_hash=VALUES(password_hash),
  display_name=VALUES(display_name), role=VALUES(role), status=VALUES(status),
  email_verified=VALUES(email_verified), verify_token=VALUES(verify_token),
  quota_tokens=VALUES(quota_tokens), quota_reset_at=VALUES(quota_reset_at), updated_at=VALUES(updated_at)`,
		u.ID, u.OrgID, u.Email, u.PasswordHash, u.DisplayName, u.Role, u.Status,
		boolToInt(u.EmailVerified), u.VerifyToken, quotaVal(u.QuotaTokens), nullTime(u.QuotaResetAt),
		nowOr(u.CreatedAt), time.Now())
	return err
}

func (r *MySQLUserRepo) FindByID(ctx context.Context, id string) (*auth.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, userCols+` WHERE id=?`, id))
}

func (r *MySQLUserRepo) FindByEmail(ctx context.Context, orgID, email string) (*auth.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, userCols+` WHERE org_id=? AND email=?`, orgID, email))
}

func (r *MySQLUserRepo) FindByVerifyToken(ctx context.Context, token string) (*auth.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, userCols+` WHERE verify_token=? AND status=?`, token, auth.StatusPending))
}

func (r *MySQLUserRepo) ListByOrg(ctx context.Context, orgID string) ([]*auth.User, error) {
	rows, err := r.db.QueryContext(ctx, userCols+` WHERE org_id=? ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*auth.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func scanUser(s scanner) (*auth.User, error) {
	var u auth.User
	var createdAt, updatedAt, quotaResetAt sql.NullTime
	var quotaTokens sql.NullInt64
	var emailVerified int
	if err := s.Scan(&u.ID, &u.OrgID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&emailVerified, &u.VerifyToken, &quotaTokens, &quotaResetAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	u.EmailVerified = emailVerified != 0
	if quotaTokens.Valid {
		u.QuotaTokens = quotaTokens.Int64
	}
	u.QuotaResetAt = quotaResetAt.Time
	u.CreatedAt = createdAt.Time
	u.UpdatedAt = updatedAt.Time
	return &u, nil
}

// ---- Organization (MySQL) ----

type MySQLOrgRepo struct{ db *sql.DB }

func NewMySQLOrgRepo(db *sql.DB) *MySQLOrgRepo { return &MySQLOrgRepo{db: db} }

var _ auth.OrgRepository = (*MySQLOrgRepo)(nil)

const orgCols = `SELECT id,name,slug,plan,owner_id,created_at,updated_at FROM organizations`

func (r *MySQLOrgRepo) Save(ctx context.Context, o *auth.Organization) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO organizations (id,name,slug,plan,owner_id,created_at,updated_at)
VALUES (?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE name=VALUES(name), slug=VALUES(slug), plan=VALUES(plan), owner_id=VALUES(owner_id), updated_at=VALUES(updated_at)`,
		o.ID, o.Name, o.Slug, o.Plan, o.OwnerID, nowOr(o.CreatedAt), time.Now())
	return err
}

func (r *MySQLOrgRepo) FindByID(ctx context.Context, id string) (*auth.Organization, error) {
	return scanOrg(r.db.QueryRowContext(ctx, orgCols+` WHERE id=?`, id))
}

func (r *MySQLOrgRepo) FindBySlug(ctx context.Context, slug string) (*auth.Organization, error) {
	return scanOrg(r.db.QueryRowContext(ctx, orgCols+` WHERE slug=?`, slug))
}

func scanOrg(s scanner) (*auth.Organization, error) {
	var o auth.Organization
	var createdAt, updatedAt sql.NullTime
	if err := s.Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.OwnerID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	o.CreatedAt = createdAt.Time
	o.UpdatedAt = updatedAt.Time
	return &o, nil
}

// ---- Device (MySQL) ----

type MySQLDeviceRepo struct{ db *sql.DB }

func NewMySQLDeviceRepo(db *sql.DB) *MySQLDeviceRepo { return &MySQLDeviceRepo{db: db} }

var _ auth.DeviceRepository = (*MySQLDeviceRepo)(nil)

const deviceCols = `SELECT id,user_code,user_id,org_id,status,scope,expires_at,last_poll_at,approved_at,created_at FROM devices`

func (r *MySQLDeviceRepo) Save(ctx context.Context, d *auth.Device) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO devices (id,user_code,user_id,org_id,status,scope,expires_at,last_poll_at,approved_at,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE user_id=VALUES(user_id), status=VALUES(status), last_poll_at=VALUES(last_poll_at), approved_at=VALUES(approved_at)`,
		d.ID, d.UserCode, d.UserID, d.OrgID, d.Status, d.Scope, d.ExpiresAt, nullTime(d.LastPollAt), nullTime(d.ApprovedAt), nowOr(d.CreatedAt))
	return err
}

func (r *MySQLDeviceRepo) FindByDeviceCode(ctx context.Context, deviceCode string) (*auth.Device, error) {
	return scanDevice(r.db.QueryRowContext(ctx, deviceCols+` WHERE id=?`, deviceCode))
}

func (r *MySQLDeviceRepo) FindByUserCode(ctx context.Context, userCode string) (*auth.Device, error) {
	return scanDevice(r.db.QueryRowContext(ctx, deviceCols+` WHERE user_code=?`, userCode))
}

func scanDevice(s scanner) (*auth.Device, error) {
	var d auth.Device
	var expiresAt, lastPollAt, approvedAt, createdAt sql.NullTime
	if err := s.Scan(&d.ID, &d.UserCode, &d.UserID, &d.OrgID, &d.Status, &d.Scope,
		&expiresAt, &lastPollAt, &approvedAt, &createdAt); err != nil {
		return nil, err
	}
	d.ExpiresAt = expiresAt.Time
	d.LastPollAt = lastPollAt.Time
	d.ApprovedAt = approvedAt.Time
	d.CreatedAt = createdAt.Time
	return &d, nil
}

// ---- RefreshToken (MySQL) ----

type MySQLRefreshTokenRepo struct{ db *sql.DB }

func NewMySQLRefreshTokenRepo(db *sql.DB) *MySQLRefreshTokenRepo {
	return &MySQLRefreshTokenRepo{db: db}
}

var _ auth.RefreshTokenRepository = (*MySQLRefreshTokenRepo)(nil)

const refreshCols = `SELECT id,user_id,org_id,device_id,token_hash,scope,expires_at,revoked,created_at,updated_at FROM refresh_tokens`

func (r *MySQLRefreshTokenRepo) Save(ctx context.Context, t *auth.RefreshToken) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO refresh_tokens (id,user_id,org_id,device_id,token_hash,scope,expires_at,revoked,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE user_id=VALUES(user_id), org_id=VALUES(org_id), device_id=VALUES(device_id),
  token_hash=VALUES(token_hash), scope=VALUES(scope), expires_at=VALUES(expires_at), revoked=VALUES(revoked), updated_at=VALUES(updated_at)`,
		t.ID, t.UserID, t.OrgID, t.DeviceID, t.TokenHash, t.Scope, t.ExpiresAt, boolToInt(t.Revoked), nowOr(t.CreatedAt), time.Now())
	return err
}

func (r *MySQLRefreshTokenRepo) FindByID(ctx context.Context, jid string) (*auth.RefreshToken, error) {
	return scanRefresh(r.db.QueryRowContext(ctx, refreshCols+` WHERE id=? AND revoked=0`, jid))
}

func (r *MySQLRefreshTokenRepo) FindByHash(ctx context.Context, tokenHash string) (*auth.RefreshToken, error) {
	return scanRefresh(r.db.QueryRowContext(ctx, refreshCols+` WHERE token_hash=? AND revoked=0`, tokenHash))
}

func (r *MySQLRefreshTokenRepo) Revoke(ctx context.Context, jid string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked=1, updated_at=? WHERE id=?`, time.Now(), jid)
	return err
}

func (r *MySQLRefreshTokenRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked=1, updated_at=? WHERE user_id=?`, time.Now(), userID)
	return err
}

func scanRefresh(s scanner) (*auth.RefreshToken, error) {
	var t auth.RefreshToken
	var expiresAt, createdAt, updatedAt sql.NullTime
	var revoked int
	if err := s.Scan(&t.ID, &t.UserID, &t.OrgID, &t.DeviceID, &t.TokenHash, &t.Scope,
		&expiresAt, &revoked, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	t.ExpiresAt = expiresAt.Time
	t.Revoked = revoked != 0
	t.CreatedAt = createdAt.Time
	t.UpdatedAt = updatedAt.Time
	return &t, nil
}
