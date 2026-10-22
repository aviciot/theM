package llmgateway

import (
	"net/http"

	"github.com/aviciot/them/internal/auth"
)

// OptionalJWTMiddleware attempts to validate JWT from X-User-JWT header.
// If present and valid, stores claims in request context via auth.WithClaims.
// If absent or invalid, continues without JWT (falls back to token-based auth).
// This allows Gateway to support BOTH:
//   - Token-based auth (Authorization: Bearer {gateway_token})
//   - User identification via JWT (X-User-JWT: {user_jwt})
func OptionalJWTMiddleware(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try X-User-JWT header (separate from Authorization header which has gateway token)
			userJWT := r.Header.Get("X-User-JWT")
			if userJWT != "" {
				claims, err := auth.ValidateHS256JWT(userJWT, jwtSecret)
				if err == nil {
					// Valid JWT - store claims in context
					ctx := auth.WithClaims(r.Context(), claims)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Invalid JWT - log but continue (don't fail the request)
			}

			// No JWT or invalid JWT - continue without user_id
			next.ServeHTTP(w, r)
		})
	}
}
