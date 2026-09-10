package auth

// ExternalJWTValidator validates RS256 JWTs issued by an external IdP (e.g. a
// bank's Keycloak) at runtime entry points with access_mode="external_jwt".
//
// This is separate from the OIDC SSO flow (authserver/oidc*.go) which validates
// ID tokens for dashboard login.  Here we validate access tokens presented
// directly at WS/SSE entry points by end users of the bank's customer-facing app.
//
// Algorithm support: RS256 only.  ES256 is a documented future addition.
//
// Trust boundary:
//   - ExternalUserID is extracted exclusively from the validated JWT sub claim.
//   - X-External-User headers are never read in this path.
//   - TenantID comes from the entry point's server-resolved config, never from the JWT.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aviciot/them/internal/jwks"
)

// ExternalJWTClaims are the claims extracted from a validated external JWT.
// Only the fields required for runtime identity and validation are decoded.
// The email field is intentionally omitted — customer tokens from bank IdPs
// may not include email, unlike OIDC ID tokens for dashboard SSO.
type ExternalJWTClaims struct {
	Sub      string   `json:"sub"`
	Iss      string   `json:"iss"`
	Aud      audience `json:"aud"` // string or []string
	Exp      int64    `json:"exp"`
	Nbf      int64    `json:"nbf"`
	Iat      int64    `json:"iat"`
}

// audience handles the JWT "aud" claim which may be a single string or an array.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	// Try array first.
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*a = arr
		return nil
	}
	// Fall back to single string.
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("external_jwt: aud claim is neither string nor []string")
	}
	*a = []string{s}
	return nil
}

// ExternalJWTConfig holds the per-tenant configuration for external JWT validation.
// Loaded from them.tenant_runtime_config at admission time.
type ExternalJWTConfig struct {
	JWKSUri  string // HTTPS URL of the IdP's JWKS endpoint
	Issuer   string // expected "iss" claim value
	Audience string // expected "aud" claim value; empty = skip aud validation
	SubClaim string // claim name to use as ExternalUserID; default "sub"
}

// ExternalJWTValidator validates tokens using the shared JWKS cache.
type ExternalJWTValidator struct {
	cache *jwks.Cache
}

// NewExternalJWTValidator creates a validator with a shared JWKS cache.
// The same validator instance should be shared across all tenants — the cache
// is keyed by JWKS URI, so per-tenant isolation is maintained automatically.
func NewExternalJWTValidator(cache *jwks.Cache) *ExternalJWTValidator {
	return &ExternalJWTValidator{cache: cache}
}

// Validate verifies the RS256-signed token against the tenant's JWKS and
// returns the external user ID (from the "sub" claim by default).
//
// Errors are safe to log but must not be returned verbatim to callers as they
// may reveal internal IdP configuration details.
func (v *ExternalJWTValidator) Validate(ctx context.Context, rawToken string, cfg ExternalJWTConfig) (externalUserID string, err error) {
	payloadBytes, err := jwks.VerifyRS256(ctx, v.cache, cfg.JWKSUri, rawToken)
	if err != nil {
		return "", err
	}

	var claims ExternalJWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", fmt.Errorf("external_jwt: parse claims: %w", err)
	}

	// Validate standard claims.
	now := time.Now().Unix()
	if claims.Exp > 0 && now > claims.Exp {
		return "", fmt.Errorf("external_jwt: token expired (exp=%d, now=%d)", claims.Exp, now)
	}
	if claims.Nbf > 0 && now < claims.Nbf {
		return "", fmt.Errorf("external_jwt: token not yet valid (nbf=%d, now=%d)", claims.Nbf, now)
	}
	if claims.Iss != cfg.Issuer {
		return "", fmt.Errorf("external_jwt: issuer mismatch (got %q, want %q)", claims.Iss, cfg.Issuer)
	}
	if cfg.Audience != "" {
		found := false
		for _, a := range claims.Aud {
			if a == cfg.Audience {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("external_jwt: audience mismatch (got %v, want %q)", []string(claims.Aud), cfg.Audience)
		}
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("external_jwt: missing sub claim")
	}

	return claims.Sub, nil
}
