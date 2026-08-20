package repository

import (
	"context"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/auth"
)

// ---- User (in-memory) ----

type MemoryUserRepo struct {
	mu    sync.RWMutex
	byID  map[string]*auth.User
	byOrg map[string][]*auth.User
}

func NewMemoryUserRepo() *MemoryUserRepo {
	return &MemoryUserRepo{byID: make(map[string]*auth.User), byOrg: make(map[string][]*auth.User)}
}

var _ auth.UserRepository = (*MemoryUserRepo)(nil)

func (r *MemoryUserRepo) Save(_ context.Context, u *auth.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *u
	r.byID[u.ID] = &cp
	r.byOrg[u.OrgID] = append(r.byOrg[u.OrgID], &cp)
	return nil
}

func (r *MemoryUserRepo) FindByID(_ context.Context, id string) (*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if u, ok := r.byID[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, nil
}

func (r *MemoryUserRepo) FindByEmail(_ context.Context, orgID, email string) (*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.byID {
		if u.OrgID == orgID && u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *MemoryUserRepo) FindByVerifyToken(_ context.Context, token string) (*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.byID {
		if u.VerifyToken == token && u.Status == auth.StatusPending {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *MemoryUserRepo) ListByOrg(_ context.Context, orgID string) ([]*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*auth.User, 0, len(r.byOrg[orgID]))
	for _, u := range r.byOrg[orgID] {
		cp := *u
		out = append(out, &cp)
	}
	return out, nil
}

// ---- Organization (in-memory) ----

type MemoryOrgRepo struct {
	mu     sync.RWMutex
	byID   map[string]*auth.Organization
	bySlug map[string]*auth.Organization
}

func NewMemoryOrgRepo() *MemoryOrgRepo {
	return &MemoryOrgRepo{byID: make(map[string]*auth.Organization), bySlug: make(map[string]*auth.Organization)}
}

var _ auth.OrgRepository = (*MemoryOrgRepo)(nil)

func (r *MemoryOrgRepo) Save(_ context.Context, o *auth.Organization) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *o
	r.byID[o.ID] = &cp
	r.bySlug[o.Slug] = &cp
	return nil
}

func (r *MemoryOrgRepo) FindByID(_ context.Context, id string) (*auth.Organization, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if o, ok := r.byID[id]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

func (r *MemoryOrgRepo) FindBySlug(_ context.Context, slug string) (*auth.Organization, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if o, ok := r.bySlug[slug]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

// ---- Device (in-memory) ----

type MemoryDeviceRepo struct {
	mu        sync.RWMutex
	byCode    map[string]*auth.Device
	byUserCode map[string]*auth.Device
}

func NewMemoryDeviceRepo() *MemoryDeviceRepo {
	return &MemoryDeviceRepo{byCode: make(map[string]*auth.Device), byUserCode: make(map[string]*auth.Device)}
}

var _ auth.DeviceRepository = (*MemoryDeviceRepo)(nil)

func (r *MemoryDeviceRepo) Save(_ context.Context, d *auth.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *d
	r.byCode[d.ID] = &cp
	r.byUserCode[d.UserCode] = &cp
	return nil
}

func (r *MemoryDeviceRepo) FindByDeviceCode(_ context.Context, deviceCode string) (*auth.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d, ok := r.byCode[deviceCode]; ok {
		cp := *d
		return &cp, nil
	}
	return nil, nil
}

func (r *MemoryDeviceRepo) FindByUserCode(_ context.Context, userCode string) (*auth.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d, ok := r.byUserCode[userCode]; ok {
		cp := *d
		return &cp, nil
	}
	return nil, nil
}

// ---- RefreshToken (in-memory) ----

type MemoryRefreshTokenRepo struct {
	mu   sync.RWMutex
	byID map[string]*auth.RefreshToken
}

func NewMemoryRefreshTokenRepo() *MemoryRefreshTokenRepo {
	return &MemoryRefreshTokenRepo{byID: make(map[string]*auth.RefreshToken)}
}

var _ auth.RefreshTokenRepository = (*MemoryRefreshTokenRepo)(nil)

func (r *MemoryRefreshTokenRepo) Save(_ context.Context, t *auth.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *t
	r.byID[t.ID] = &cp
	return nil
}

func (r *MemoryRefreshTokenRepo) FindByID(_ context.Context, jid string) (*auth.RefreshToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byID[jid]
	if !ok || t.Revoked {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (r *MemoryRefreshTokenRepo) FindByHash(_ context.Context, tokenHash string) (*auth.RefreshToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.byID {
		if t.TokenHash == tokenHash && !t.Revoked {
			cp := *t
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *MemoryRefreshTokenRepo) Revoke(_ context.Context, jid string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.byID[jid]; ok {
		t.Revoked = true
	}
	return nil
}

func (r *MemoryRefreshTokenRepo) RevokeAllForUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.byID {
		if t.UserID == userID {
			t.Revoked = true
		}
	}
	return nil
}
