package ratelimit

import (
	"context"
	"fmt"
	"time"
)

// UserLimiter enforces per-user rate limits (requests per minute).
type UserLimiter struct {
	redis RedisIncrementer
}

// NewUserLimiter creates a UserLimiter backed by Redis.
func NewUserLimiter(redis RedisIncrementer) *UserLimiter {
	return &UserLimiter{redis: redis}
}

// CheckLimit returns true if the user has exceeded their rate limit.
// rpm is the role's rate_limit (e.g., 5000 for developer, 100 for viewer).
// If rpm <= 0, the limit is considered unlimited (always returns false).
func (ul *UserLimiter) CheckLimit(ctx context.Context, userID int64, rpm int) (bool, error) {
	if rpm <= 0 {
		return false, nil // 0 or negative = unlimited
	}

	now := time.Now()
	minute := now.Truncate(time.Minute).Unix()
	key := fmt.Sprintf("rl:user:%d:minute:%d", userID, minute)

	count, err := ul.redis.Incr(ctx, key)
	if err != nil {
		return false, fmt.Errorf("user rate limit incr %s: %w", key, err)
	}

	// Set TTL on every increment (idempotent for existing keys, necessary for new ones)
	_ = ul.redis.Expire(ctx, key, 90*time.Second)

	return count > int64(rpm), nil
}
