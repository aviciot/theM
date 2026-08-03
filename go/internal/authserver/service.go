package authserver

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Service-level sentinel errors. Handlers map these to HTTP status codes.
var (
	// ErrInvalidCredentials — bad password / unknown user / bad api key. 401.
	ErrInvalidCredentials = errors.New("authserver: invalid credentials")
	// ErrMissingCredentials — neither api_key nor username+password supplied. 400.
	ErrMissingCredentials = errors.New("authserver: missing credentials")
	// ErrDashboardAccessDenied — role has dashboard_access 'none'. 403.
	ErrDashboardAccessDenied = errors.New("authserver: dashboard access denied")
	// ErrNotAuthenticated — no/invalid token where one is required. 401.
	ErrNotAuthenticated = errors.New("authserver: not authenticated")
	// ErrTokenRevoked — token is blacklisted. 401.
	ErrTokenRevoked = errors.New("authserver: token revoked")
)

// Service holds the auth business logic. It is transport-agnostic: handlers call
// its methods and translate the results and sentinel errors to HTTP.
type Service struct {
	store  Store
	signer *tokenSigner
	cfg    *Config
	log    *slog.Logger
}

// NewService wires the service with its store, signer, config, and logger.
func NewService(store Store, cfg *Config, log *slog.Logger) *Service {
	return &Service{
		store:  store,
		signer: newTokenSigner([]byte(cfg.JWTSecret), cfg.AccessTokenExpiry, cfg.RefreshTokenExpiry),
		cfg:    cfg,
		log:    log,
	}
}

// LoginInput carries the two supported login methods.
type LoginInput struct {
	Username string
	Password string
	APIKey   string
}

// TokenPair is the login/refresh response payload.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// PublicUser is the /me response payload.
type PublicUser struct {
	ID       int64
	Email    string
	Name     string
	Username string
	Role     string
}

// Login authenticates via api key OR username/password, enforces the dashboard
// access gate, and issues an access+refresh token pair. Never logs secrets.
func (s *Service) Login(ctx context.Context, in LoginInput) (*TokenPair, error) {
	var user *userRecord

	switch {
	case in.APIKey != "":
		u, err := s.store.GetUserByAPIKeyHash(ctx, hashToken(in.APIKey))
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		if err != nil {
			return nil, err
		}
		user = u
	case in.Username != "" && in.Password != "":
		u, err := s.store.GetUserByLogin(ctx, in.Username)
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		if err != nil {
			return nil, err
		}
		if !verifyPassword(in.Password, u.PasswordHash) {
			return nil, ErrInvalidCredentials
		}
		user = u
	default:
		return nil, ErrMissingCredentials
	}

	if user.DashboardAccess == "" || user.DashboardAccess == "none" {
		s.log.Warn("login rejected: dashboard access denied", "user_id", user.ID, "dashboard_access", user.DashboardAccess)
		return nil, ErrDashboardAccessDenied
	}

	return s.issuePair(ctx, user)
}

// Refresh validates a refresh token and issues a fresh pair. The token must be of
// type "refresh" and must not be blacklisted.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	if refreshToken == "" {
		return nil, ErrNotAuthenticated
	}
	claims, err := s.verifyUsable(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Type != "refresh" {
		return nil, ErrWrongTokenType
	}
	user, err := s.store.GetUserByID(ctx, claims.UserID())
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrNotAuthenticated
	}
	if err != nil {
		return nil, err
	}
	return s.issuePair(ctx, user)
}

// Me resolves the current user from an access token (typically the cookie value).
func (s *Service) Me(ctx context.Context, accessToken string) (*PublicUser, error) {
	if accessToken == "" {
		return nil, ErrNotAuthenticated
	}
	claims, err := s.verifyUsable(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	user, err := s.store.GetUserByID(ctx, claims.UserID())
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrNotAuthenticated
	}
	if err != nil {
		return nil, err
	}
	return &PublicUser{
		ID:       user.ID,
		Email:    user.Email,
		Name:     user.Name,
		Username: user.Username,
		Role:     user.Role,
	}, nil
}

// Verify validates a bearer access token for service-to-service callers and
// returns the fresh user record. Mirrors Python /verify.
func (s *Service) Verify(ctx context.Context, accessToken string) (*PublicUser, error) {
	return s.Me(ctx, accessToken)
}

// Logout blacklists the given access token until its natural expiry. It is
// best-effort: an unparsable token is a no-op (matches Python).
func (s *Service) Logout(ctx context.Context, accessToken string) {
	if accessToken == "" {
		return
	}
	claims, err := s.signer.Verify(accessToken)
	if err != nil {
		// Even expired/invalid tokens: nothing to revoke. Do not error the logout.
		return
	}
	exp := time.Unix(claims.Exp, 0)
	if err := s.store.Blacklist(ctx, hashToken(accessToken), exp); err != nil {
		s.log.Warn("logout: failed to blacklist token", "user_id", claims.UserID(), "error", err)
	}
}

// issuePair mints an access+refresh pair, honours the role token_expiry, and
// records the session best-effort.
func (s *Service) issuePair(ctx context.Context, user *userRecord) (*TokenPair, error) {
	access, expiresIn, err := s.signer.IssueAccessToken(user.ID, user.Username, user.Name, user.Role, user.TokenExpiry)
	if err != nil {
		return nil, err
	}
	refresh, err := s.signer.IssueRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}

	if err := s.store.InsertSession(ctx, user.ID, hashToken(access), time.Now().Add(time.Duration(expiresIn)*time.Second)); err != nil {
		s.log.Warn("login: failed to record session", "user_id", user.ID, "error", err)
	}
	if err := s.store.TouchLastLogin(ctx, user.ID); err != nil {
		s.log.Warn("login: failed to update last_login_at", "user_id", user.ID, "error", err)
	}

	return &TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: expiresIn}, nil
}

// verifyUsable verifies signature+expiry and checks the blacklist, translating
// low-level token errors to service sentinels.
func (s *Service) verifyUsable(ctx context.Context, token string) (*verifiedClaims, error) {
	claims, err := s.signer.Verify(token)
	if err != nil {
		return nil, ErrNotAuthenticated
	}
	blacklisted, err := s.store.IsBlacklisted(ctx, hashToken(token))
	if err != nil {
		return nil, err
	}
	if blacklisted {
		return nil, ErrTokenRevoked
	}
	return claims, nil
}
