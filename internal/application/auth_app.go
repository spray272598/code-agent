package application

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/auth"
	"golang.org/x/crypto/bcrypt"
)

// EmailSender delivers verification / notification emails.
// The default console sender logs the link; production swaps in SMTP/SES.
type EmailSender interface {
	SendVerification(ctx context.Context, to, link string) error
	SendPasswordReset(ctx context.Context, to, link string) error
}

// consoleEmailSender is the safe no-op default: logs the verification link so
// local/dev flows work without an external mail provider.
type consoleEmailSender struct{}

func (consoleEmailSender) SendVerification(_ context.Context, to, link string) error {
	log.Printf("[email:console] verification for %s: %s", to, link)
	return nil
}

func (consoleEmailSender) SendPasswordReset(_ context.Context, to, link string) error {
	log.Printf("[email:console] password reset for %s: %s", to, link)
	return nil
}

// AuthService handles the account lifecycle: organization + owner signup,
// member registration, email verification, and credential authentication.
// JWT issuance is added in Sprint 1.3.
type AuthService struct {
	users         auth.UserRepository
	orgs          auth.OrgRepository
	email         EmailSender
	bcryptCost    int
	verifyBaseURL string
}

// NewAuthService wires the account repos. Pass nil email to use the console sender.
func NewAuthService(users auth.UserRepository, orgs auth.OrgRepository, email EmailSender) *AuthService {
	if email == nil {
		email = consoleEmailSender{}
	}
	return &AuthService{
		users:         users,
		orgs:          orgs,
		email:         email,
		bcryptCost:    bcrypt.DefaultCost,
		verifyBaseURL: "https://app.example.com",
	}
}

// WithVerifyBaseURL overrides the base URL used in verification links.
func (s *AuthService) WithVerifyBaseURL(u string) *AuthService {
	if u != "" {
		s.verifyBaseURL = strings.TrimRight(u, "/")
	}
	return s
}

// Signup creates a new organization and its owner account (pending verification).
func (s *AuthService) Signup(ctx context.Context, email, password, displayName, orgName string) (*auth.Organization, *auth.User, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return nil, nil, errors.New("invalid email")
	}
	if len(password) < 8 {
		return nil, nil, errors.New("password must be at least 8 characters")
	}
	if strings.TrimSpace(orgName) == "" {
		return nil, nil, errors.New("organization name required")
	}

	base := slugify(orgName)
	slug := base
	for i := 0; i < 5; i++ {
		existing, err := s.orgs.FindBySlug(ctx, slug)
		if err != nil {
			return nil, nil, err
		}
		if existing == nil {
			break
		}
		slug = base + "-" + auth.RandomToken(4)
	}

	org := &auth.Organization{
		ID:        auth.NewULID(),
		Name:      strings.TrimSpace(orgName),
		Slug:      slug,
		Plan:      auth.PlanFree,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.orgs.Save(ctx, org); err != nil {
		return nil, nil, err
	}
	u, err := s.createUser(ctx, org.ID, email, password, displayName, auth.RoleOwner)
	if err != nil {
		return nil, nil, err
	}
	return org, u, nil
}

// Register adds a member account to an existing organization (invite flow).
func (s *AuthService) Register(ctx context.Context, orgID, email, password, displayName string) (*auth.User, error) {
	if orgID == "" {
		return nil, errors.New("org_id required")
	}
	org, err := s.orgs.FindByID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, errors.New("organization not found")
	}
	return s.createUser(ctx, orgID, email, password, displayName, auth.RoleMember)
}

// createUser hashes the password, persists a pending user, and triggers the
// verification email. Quota defaults to unlimited (-1).
func (s *AuthService) createUser(ctx context.Context, orgID, email, password, displayName, role string) (*auth.User, error) {
	if existing, _ := s.users.FindByEmail(ctx, orgID, email); existing != nil {
		return nil, errors.New("email already registered in this organization")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return nil, err
	}
	u := &auth.User{
		ID:            auth.NewULID(),
		OrgID:         orgID,
		Email:         email,
		PasswordHash:  string(hash),
		DisplayName:   displayName,
		Role:          role,
		Status:        auth.StatusPending,
		EmailVerified: false,
		VerifyToken:   auth.RandomToken(32),
		QuotaTokens:   -1, // unlimited until an admin sets a budget
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	link := s.verifyBaseURL + "/verify?token=" + u.VerifyToken
	_ = s.email.SendVerification(ctx, u.Email, link)
	return u, nil
}

// VerifyEmail activates a pending account using its verification token.
func (s *AuthService) VerifyEmail(ctx context.Context, token string) (*auth.User, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("verification token required")
	}
	u, err := s.users.FindByVerifyToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errors.New("invalid or expired verification token")
	}
	u.Status = auth.StatusActive
	u.EmailVerified = true
	u.VerifyToken = ""
	u.UpdatedAt = time.Now()
	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// OrgRepo returns the underlying organization repository so the HTTP layer can
// resolve a slug to an id without leaking the field out of the package.
func (s *AuthService) OrgRepo() auth.OrgRepository { return s.orgs }

// GetUser returns a user by id (nil when not found).
func (s *AuthService) GetUser(ctx context.Context, userID string) (*auth.User, error) {
	if userID == "" {
		return nil, errors.New("user_id required")
	}
	return s.users.FindByID(ctx, userID)
}

// UpdateProfile updates the display name of an existing user.
func (s *AuthService) UpdateProfile(ctx context.Context, userID, displayName string) (*auth.User, error) {
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errors.New("user not found")
	}
	u.DisplayName = strings.TrimSpace(displayName)
	u.UpdatedAt = time.Now()
	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// ChangePassword verifies the current password and replaces it with the new one.
func (s *AuthService) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return errors.New("user not found")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPassword)) != nil {
		return errors.New("current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	u.UpdatedAt = time.Now()
	return s.users.Save(ctx, u)
}

// RequestPasswordReset issues a time-limited reset token and emails the link.
// It always succeeds from the caller's perspective when the org+email is unknown,
// to avoid leaking which accounts exist.
func (s *AuthService) RequestPasswordReset(ctx context.Context, orgID, email string) error {
	email = normalizeEmail(email)
	u, err := s.users.FindByEmail(ctx, orgID, email)
	if err != nil {
		return err
	}
	if u == nil || u.Status != auth.StatusActive {
		return nil // do not reveal whether the account exists
	}
	u.ResetToken = auth.RandomToken(32)
	u.ResetExpiresAt = time.Now().Add(30 * time.Minute)
	u.UpdatedAt = time.Now()
	if err := s.users.Save(ctx, u); err != nil {
		return err
	}
	link := s.verifyBaseURL + "/reset?token=" + u.ResetToken
	return s.email.SendPasswordReset(ctx, u.Email, link)
}

// ResetPassword consumes a valid reset token and sets a new password.
func (s *AuthService) ResetPassword(ctx context.Context, token, newPassword string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("reset token required")
	}
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}
	u, err := s.users.FindByResetToken(ctx, token)
	if err != nil {
		return err
	}
	if u == nil {
		return errors.New("invalid or expired reset token")
	}
	if u.ResetExpiresAt.IsZero() || time.Now().After(u.ResetExpiresAt) {
		return errors.New("invalid or expired reset token")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	u.ResetToken = ""
	u.ResetExpiresAt = time.Time{}
	u.UpdatedAt = time.Now()
	return s.users.Save(ctx, u)
}

// AuthenticatePassword verifies credentials for an active account within an org.
// It returns the same generic error for unknown users, inactive accounts, and
// wrong passwords to avoid user enumeration.
func (s *AuthService) AuthenticatePassword(ctx context.Context, orgID, email, password string) (*auth.User, error) {
	email = normalizeEmail(email)
	u, err := s.users.FindByEmail(ctx, orgID, email)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != auth.StatusActive {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, errors.New("invalid credentials")
	}
	return u, nil
}

func normalizeEmail(e string) string {
	return strings.TrimSpace(strings.ToLower(e))
}

func validEmail(e string) bool {
	return strings.Contains(e, "@") && strings.Contains(e, ".") && len(e) <= 254
}

// slugify converts an org name to a URL-safe slug (a-z0-9 and dashes).
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "org"
	}
	return out
}
