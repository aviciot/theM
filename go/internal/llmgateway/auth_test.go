package llmgateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/auth"
)

// TODO: Add proper JWT generation tests once we have a helper function in the auth package.
// The middleware logic is straightforward (try to validate, continue on failure), so integration
// tests with real JWTs from the auth service will provide better coverage.

// TestOptionalJWTMiddleware_NoJWT verifies that missing JWT doesn't fail the request.
func TestOptionalJWTMiddleware_NoJWT(t *testing.T) {
	secret := []byte("test-secret-key-12345678")

	// Create test handler
	var claimsPresent bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, claimsPresent = auth.ClaimsFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with JWT middleware
	mw := OptionalJWTMiddleware(secret)
	wrappedHandler := mw(handler)

	// Make request WITHOUT X-User-JWT header
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rec := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	// Assert: no claims in context, but request succeeds
	if claimsPresent {
		t.Error("expected no claims in context, but claims were present")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

// TestOptionalJWTMiddleware_InvalidJWT verifies that invalid JWT doesn't fail the request.
func TestOptionalJWTMiddleware_InvalidJWT(t *testing.T) {
	secret := []byte("test-secret-key-12345678")

	// Create test handler
	var claimsPresent bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, claimsPresent = auth.ClaimsFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with JWT middleware
	mw := OptionalJWTMiddleware(secret)
	wrappedHandler := mw(handler)

	// Make request with invalid JWT
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set("X-User-JWT", "invalid.jwt.token")
	rec := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	// Assert: no claims in context (invalid JWT ignored), but request succeeds
	if claimsPresent {
		t.Error("expected no claims in context for invalid JWT, but claims were present")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 (continue despite invalid JWT), got %d", rec.Code)
	}
}
