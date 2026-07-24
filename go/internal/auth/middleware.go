package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
)

// ──────────────────────────────────────────────────────────────────────────────
// Context keys
// ──────────────────────────────────────────────────────────────────────────────

// contextKey is an unexported type for context keys defined in this package.
// Using a distinct type prevents collisions with keys defined in other packages.
type contextKey int

const (
	tokenInfoKey contextKey = iota
	claimsKey
)

// ──────────────────────────────────────────────────────────────────────────────
// Context accessors
// ──────────────────────────────────────────────────────────────────────────────

// TokenInfoFromCtx extracts the *TokenInfo stored in ctx by BearerMiddleware.
// Returns (nil, false) if no TokenInfo is present.
func TokenInfoFromCtx(ctx context.Context) (*TokenInfo, bool) {
	v, ok := ctx.Value(tokenInfoKey).(*TokenInfo)
	return v, ok && v != nil
}

// ClaimsFromCtx extracts the *Claims stored in ctx by JWTMiddleware.
// Returns (nil, false) if no Claims are present.
func ClaimsFromCtx(ctx context.Context) (*Claims, bool) {
	v, ok := ctx.Value(claimsKey).(*Claims)
	return v, ok && v != nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Middleware constructors
// ──────────────────────────────────────────────────────────────────────────────

// BearerMiddleware returns an http.Handler middleware that validates the
// Authorization: Bearer <token> header against the provided Cache.
//
// On success:  *TokenInfo is stored in the request context; the next handler
//              is called.
// On failure:  A 401 JSON response is written and the chain is terminated.
//
// Routes that must remain unauthenticated (e.g. /health, /metrics) must NOT
// be wrapped with this middleware.
func BearerMiddleware(cache *Cache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractBearer(r)
			if !ok {
				writeUnauthorized(w, "missing or malformed Authorization header")
				return
			}

			info, err := cache.Validate(r.Context(), raw)
			if err != nil {
				writeUnauthorized(w, "invalid or revoked bearer token")
				return
			}

			ctx := context.WithValue(r.Context(), tokenInfoKey, info)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// JWTMiddleware returns an http.Handler middleware that validates the
// Authorization: Bearer <token> header as an RS256 JWT signed by pubKey.
//
// On success:  *Claims is stored in the request context; the next handler
//              is called.
// On failure:  A 401 JSON response is written and the chain is terminated.
//
// If pubKey is nil (JWT_PUBLIC_KEY_PEM not configured) the middleware panics
// at construction time — callers must guard with cfg.JWTPublicKey != nil before
// registering JWT-protected routes.
func JWTMiddleware(pubKey *rsa.PublicKey) func(http.Handler) http.Handler {
	if pubKey == nil {
		panic("auth: JWTMiddleware: pubKey must not be nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractBearer(r)
			if !ok {
				writeUnauthorized(w, "missing or malformed Authorization header")
				return
			}

			claims, err := ValidateJWT(raw, pubKey)
			if err != nil {
				writeUnauthorized(w, "invalid JWT: "+err.Error())
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HS256Middleware returns an http.Handler middleware that validates the
// Authorization: Bearer <token> header as an HS256 JWT signed by secret.
// This is the production path — the auth service issues HS256 tokens using
// the platform SECRET_KEY.
//
// On success:  *Claims is stored in the request context; the next handler
//              is called.
// On failure:  A 401 JSON response is written and the chain is terminated.
func HS256Middleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractBearer(r)
			if !ok {
				writeUnauthorized(w, "authentication required")
				return
			}

			claims, err := ValidateHS256JWT(raw, secret)
			if err != nil {
				writeUnauthorized(w, "invalid token: "+err.Error())
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// extractBearer parses "Authorization: Bearer <token>" and returns the token
// string. Returns ("", false) if the header is absent or malformed.
func extractBearer(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimSpace(auth[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// errResponse is the JSON shape returned on 401 errors.
type errResponse struct {
	Error string `json:"error"`
}

// writeUnauthorized writes a 401 JSON response and sets Content-Type.
func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, _ := json.Marshal(errResponse{Error: msg})
	_, _ = w.Write(body)
}
