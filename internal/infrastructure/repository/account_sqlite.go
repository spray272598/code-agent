package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
)

// sqliteTime returns nil for the zero time, else RFC3339Nano for SQLite TEXT columns.
func sqliteTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---- User (SQLite) ----

type SQLiteUserRepo struct{ db *sql.DB }

func NewSQLiteUserRepo(db *sql.DB) *SQLiteUserRepo { return &SQLiteUserRepo{db: db} }

var _ auth.UserRepository = (*SQLiteUserRepo)(nil)

func (r *SQLiteUserRepo) Save(ctx context.Context, u *auth.User) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO users (id,org_id,email,password_hash,display_name,role,status,email_verified,verify_token,reset_token,reset_expires_at,quota_tokens,quota_reset_at,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  org_id=excluded.org_id, email=excluded.email, password_hash=excluded.password_hash,
  display_name=excluded.display_name, role=excluded.role, status=excluded.status,
  email_verified=excluded.email_verified, verify_token=excluded.verify_token,
  reset_token=excluded.reset_token, reset_expires_at=excluded.reset_expires_at,
  quota_tokens=excluded.quota_tokens, quota_reset_at=excluded.quota_reset_at, updated_at=excluded.updated_at`,
		u.ID, u.OrgID, u.Email, u.PasswordHash, u.DisplayName, u.Role, u.Status,
		boolToInt(u.EmailVerified), u.VerifyToken, nullStr(u.ResetToken), sqliteTime(u.ResetExpiresAt),
		quotaVal(u.QuotaTokens), sqliteTime(u.QuotaResetAt),
		sqliteTime(nowOr(u.CreatedAt)), time.Now().Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteUserRepo) FindByID(ctx context.Context, id string) (*auth.User, error) {
	return scanUserSQLite(r.db.QueryRowContext(ctx, userColsSQLite+` WHERE id=?`, id))
}

func (r *SQLiteUserRepo) FindByEmail(ctx context.Context, orgID, email string) (*auth.User, error) {
	return scanUserSQLite(r.db.QueryRowContext(ctx, userColsSQLite+` WHERE org_id=? AND email=?`, orgID, email))
}

func (r *SQLiteUserRepo) FindByVerifyToken(ctx context.Context, token string) (*auth.User, error) {
	return scanUserSQLite(r.db.QueryRowContext(ctx, userColsSQLite+` WHERE verify_token=? AND status=?`, token, auth.StatusPending))
}

func (r *SQLiteUserRepo) FindByResetToken(ctx context.Context, token string) (*auth.User, error) {
	return scanUserSQLite(r.db.QueryRowContext(ctx, userColsSQLite+` WHERE reset_token=? AND status=?`, token, auth.StatusActive))
}

func (r *SQLiteUserRepo) ListByOrg(ctx context.Context, orgID string) ([]*auth.User, error) {
	rows, err := r.db.QueryContext(ctx, userColsSQLite+` WHERE org_id=? ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*auth.User
	for rows.Next() {
		u, err := scanUserSQLite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

const userColsSQLite = `SELECT id,org_id,email,password_hash,display_name,role,status,email_verified,verify_token,reset_token,reset_expires_at,quota_tokens,quota_reset_at,created_at,updated_at FROM users`

func scanUserSQLite(s scanner) (*auth.User, error) {
	var u auth.User
	var cAt, uAt, qAt, rAt string
	var quotaTokens sql.NullInt64
	var emailVerified int
	if err := s.Scan(&u.ID, &u.OrgID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&emailVerified, &u.VerifyToken, &u.ResetToken, &rAt, &quotaTokens, &qAt, &cAt, &uAt); err != nil {
		return nil, err
	}
	u.EmailVerified = emailVerified != 0
	if quotaTokens.Valid {
		u.QuotaTokens = quotaTokens.Int64
	}
	u.ResetExpiresAt = parseTime(rAt)
	u.QuotaResetAt = parseTime(qAt)
	u.CreatedAt = parseTime(cAt)
	u.UpdatedAt = parseTime(uAt)
	return &u, nil
}

// ---- Organization (SQLite) ----

type SQLiteOrgRepo struct{ db *sql.DB }

func NewSQLiteOrgRepo(db *sql.DB) *SQLiteOrgRepo { return &SQLiteOrgRepo{db: db} }

var _ auth.OrgRepository = (*SQLiteOrgRepo)(nil)

const orgColsSQLite = `SELECT id,name,slug,plan,owner_id,created_at,updated_at FROM organizations`

func (r *SQLiteOrgRepo) Save(ctx context.Context, o *auth.Organization) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO organizations (id,name,slug,plan,owner_id,created_at,updated_at)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, slug=excluded.slug, plan=excluded.plan, owner_id=excluded.owner_id, updated_at=excluded.updated_at`,
		o.ID, o.Name, o.Slug, o.Plan, o.OwnerID, sqliteTime(nowOr(o.CreatedAt)), time.Now().Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteOrgRepo) FindByID(ctx context.Context, id string) (*auth.Organization, error) {
	return scanOrgSQLite(r.db.QueryRowContext(ctx, orgColsSQLite+` WHERE id=?`, id))
}

func (r *SQLiteOrgRepo) FindBySlug(ctx context.Context, slug string) (*auth.Organization, error) {
	return scanOrgSQLite(r.db.QueryRowContext(ctx, orgColsSQLite+` WHERE slug=?`, slug))
}

func scanOrgSQLite(s scanner) (*auth.Organization, error) {
	var o auth.Organization
	var cAt, uAt string
	if err := s.Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.OwnerID, &cAt, &uAt); err != nil {
		return nil, err
	}
	o.CreatedAt = parseTime(cAt)
	o.UpdatedAt = parseTime(uAt)
	return &o, nil
}

// ---- Device (SQLite) ----

type SQLiteDeviceRepo struct{ db *sql.DB }

func NewSQLiteDeviceRepo(db *sql.DB) *SQLiteDeviceRepo { return &SQLiteDeviceRepo{db: db} }

var _ auth.DeviceRepository = (*SQLiteDeviceRepo)(nil)

const deviceColsSQLite = `SELECT id,user_code,user_id,org_id,status,scope,expires_at,last_poll_at,approved_at,created_at FROM devices`

func (r *SQLiteDeviceRepo) Save(ctx context.Context, d *auth.Device) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO devices (id,user_code,user_id,org_id,status,scope,expires_at,last_poll_at,approved_at,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET user_id=excluded.user_id, status=excluded.status, last_poll_at=excluded.last_poll_at, approved_at=excluded.approved_at`,
		d.ID, d.UserCode, d.UserID, d.OrgID, d.Status, d.Scope, sqliteTime(d.ExpiresAt), sqliteTime(d.LastPollAt), sqliteTime(d.ApprovedAt), sqliteTime(nowOr(d.CreatedAt)))
	return err
}

func (r *SQLiteDeviceRepo) FindByDeviceCode(ctx context.Context, deviceCode string) (*auth.Device, error) {
	return scanDeviceSQLite(r.db.QueryRowContext(ctx, deviceColsSQLite+` WHERE id=?`, deviceCode))
}

func (r *SQLiteDeviceRepo) FindByUserCode(ctx context.Context, userCode string) (*auth.Device, error) {
	return scanDeviceSQLite(r.db.QueryRowContext(ctx, deviceColsSQLite+` WHERE user_code=?`, userCode))
}

func scanDeviceSQLite(s scanner) (*auth.Device, error) {
	var d auth.Device
	var exp, poll, appr, cAt string
	if err := s.Scan(&d.ID, &d.UserCode, &d.UserID, &d.OrgID, &d.Status, &d.Scope,
		&exp, &poll, &appr, &cAt); err != nil {
		return nil, err
	}
	d.ExpiresAt = parseTime(exp)
	d.LastPollAt = parseTime(poll)
	d.ApprovedAt = parseTime(appr)
	d.CreatedAt = parseTime(cAt)
	return &d, nil
}

// ---- RefreshToken (SQLite) ----

type SQLiteRefreshTokenRepo struct{ db *sql.DB }

func NewSQLiteRefreshTokenRepo(db *sql.DB) *SQLiteRefreshTokenRepo {
	return &SQLiteRefreshTokenRepo{db: db}
}

var _ auth.RefreshTokenRepository = (*SQLiteRefreshTokenRepo)(nil)

const refreshColsSQLite = `SELECT id,user_id,org_id,device_id,token_hash,scope,expires_at,revoked,created_at,updated_at FROM refresh_tokens`

func (r *SQLiteRefreshTokenRepo) Save(ctx context.Context, t *auth.RefreshToken) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO refresh_tokens (id,user_id,org_id,device_id,token_hash,scope,expires_at,revoked,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET user_id=excluded.user_id, org_id=excluded.org_id, device_id=excluded.device_id,
  token_hash=excluded.token_hash, scope=excluded.scope, expires_at=excluded.expires_at, revoked=excluded.revoked, updated_at=excluded.updated_at`,
		t.ID, t.UserID, t.OrgID, t.DeviceID, t.TokenHash, t.Scope, sqliteTime(t.ExpiresAt), boolToInt(t.Revoked), sqliteTime(nowOr(t.CreatedAt)), time.Now().Format(time.RFC3339Nano))
	return err
}

func (r *SQLiteRefreshTokenRepo) FindByID(ctx context.Context, jid string) (*auth.RefreshToken, error) {
	return scanRefreshSQLite(r.db.QueryRowContext(ctx, refreshColsSQLite+` WHERE id=? AND revoked=0`, jid))
}

func (r *SQLiteRefreshTokenRepo) FindByHash(ctx context.Context, tokenHash string) (*auth.RefreshToken, error) {
	return scanRefreshSQLite(r.db.QueryRowContext(ctx, refreshColsSQLite+` WHERE token_hash=? AND revoked=0`, tokenHash))
}

func (r *SQLiteRefreshTokenRepo) Revoke(ctx context.Context, jid string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked=1, updated_at=? WHERE id=?`, time.Now().Format(time.RFC3339Nano), jid)
	return err
}

func (r *SQLiteRefreshTokenRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked=1, updated_at=? WHERE user_id=?`, time.Now().Format(time.RFC3339Nano), userID)
	return err
}

func scanRefreshSQLite(s scanner) (*auth.RefreshToken, error) {
	var t auth.RefreshToken
	var exp, cAt, uAt string
	var revoked int
	if err := s.Scan(&t.ID, &t.UserID, &t.OrgID, &t.DeviceID, &t.TokenHash, &t.Scope,
		&exp, &revoked, &cAt, &uAt); err != nil {
		return nil, err
	}
	t.ExpiresAt = parseTime(exp)
	t.Revoked = revoked != 0
	t.CreatedAt = parseTime(cAt)
	t.UpdatedAt = parseTime(uAt)
	return &t, nil
}
