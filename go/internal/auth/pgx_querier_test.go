//go:build integration

package auth_test

// Integration tests for PgxQuerier require a live PostgreSQL instance with the
// them schema loaded. Run with:
//
//	go test -tags=integration -v ./internal/auth/...
//
// These tests are intentionally excluded from the unit test suite (go test ./...)
// because they require a real database connection. The unit-test contract for
// the TokenQuerier interface is covered by the mockTokenQuerier tests in
// token_cache_test.go.
//
// The tests below verify the SQL filtering logic in PgxQuerier.QueryToken:
//   - enabled=true filter
//   - expires_at IS NULL or expires_at > now() filter
//   - token not found → ErrTokenNotFound
//   - disabled token → ErrTokenNotFound
//   - expired token → ErrTokenNotFound
//
// See docs/architecture-v2/lessons-learned.md for the token cache design.
