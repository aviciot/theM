package cache_test

import (
	"testing"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/cache"
)

// TestAuthRedisClient_ImplementsInterface is a compile-time check that
// *AuthRedisClient satisfies auth.RedisClient. The behavioural contract of
// auth.RedisClient is tested in internal/auth/token_cache_test.go via the
// mockRedis fake.
func TestAuthRedisClient_ImplementsInterface(t *testing.T) {
	t.Helper()
	// nil is safe here: we only need the type assertion at compile time.
	// NewAuthRedisClient panics on nil — use a typed nil cast instead.
	var _ auth.RedisClient = (*cache.AuthRedisClient)(nil)
}
