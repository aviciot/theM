package service

import "context"

// TokenGenerator is the interface the token CRUD service will use to issue
// opaque bearer tokens. Implemented by the auth package in Wave 5.
//
// Wave 5: wire this interface to auth.TokenIssuer and add
// CreateToken / RevokeToken / ListTokens service methods.
type TokenGenerator interface {
	Generate(ctx context.Context) (token string, err error)
}

// TokenService will own the business logic for access token CRUD in Wave 5.
// Currently a placeholder: the existing handler in internal/admin/tokens.go
// continues to call the DAL directly until the Wave 5 migration.
type TokenService struct {
	// Wave 5: add Dal and TokenGenerator fields here
}
