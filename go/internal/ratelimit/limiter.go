// Package ratelimit provides per-token and per-application rate limiting using
// Redis INCR with 1-minute windows.
//
// Redis key scheme:
//
//	rl:them:token:{hash}:{minute}   TTL 90s
//	rl:them:app:{app_id}:{minute}   TTL 90s
//
// The minute bucket is the Unix timestamp divided by 60, giving a new bucket
// every minute. Each bucket expires after 90 seconds to allow for clock skew
// while automatically cleaning up old keys.
package ratelimit

import (
	"context"
	"fmt"
	"time"
)

const (
	// keyTTL is how long a rate-limit bucket key survives in Redis.
	// 90 seconds covers the full minute plus a 30 second grace window.
	keyTTL = 90 * time.Second
)

// RedisIncrementer is the Redis interface required by the Limiter.
// The production implementation wraps rueidis; tests inject a fake.
type RedisIncrementer interface {
	// Incr atomically increments the integer at key and returns the new value.
	// If the key does not exist, it is created with value 0 before incrementing.
	Incr(ctx context.Context, key string) (int64, error)
	// Expire sets the TTL for an existing key. Best-effort — call after Incr.
	Expire(ctx context.Context, key string, ttl time.Duration) error
}

// Limiter checks rate limits using Redis INCR with 1-minute windows.
type Limiter struct {
	redis RedisIncrementer
}

// New creates a Limiter backed by the given RedisIncrementer.
func New(redis RedisIncrementer) *Limiter {
	return &Limiter{redis: redis}
}

// CheckToken checks the per-token rate limit.
// tokenHash should be the SHA-256 hex of the raw token (matching the DB hash).
// limit is the maximum number of requests allowed per minute.
// Returns (true, nil) if the request is allowed; (false, nil) if rate-limited;
// (false, err) if Redis is unavailable.
func (l *Limiter) CheckToken(ctx context.Context, tokenHash string, limit int) (bool, error) {
	key := fmt.Sprintf("rl:them:token:%s:%d", tokenHash, minuteBucket())
	return l.check(ctx, key, limit)
}

// CheckApp checks the per-application rate limit.
// appID is the application's integer ID.
// limit is the maximum number of requests allowed per minute.
// Returns (true, nil) if allowed; (false, nil) if rate-limited; (false, err) on Redis error.
func (l *Limiter) CheckApp(ctx context.Context, appID int64, limit int) (bool, error) {
	key := fmt.Sprintf("rl:them:app:%d:%d", appID, minuteBucket())
	return l.check(ctx, key, limit)
}

// check increments the counter for key and returns whether it is within limit.
func (l *Limiter) check(ctx context.Context, key string, limit int) (bool, error) {
	count, err := l.redis.Incr(ctx, key)
	if err != nil {
		return false, fmt.Errorf("ratelimit: incr %s: %w", key, err)
	}

	// Set TTL on every increment (idempotent for existing keys, necessary for new ones).
	// Best-effort — a TTL failure does not block the request.
	_ = l.redis.Expire(ctx, key, keyTTL)

	return count <= int64(limit), nil
}

// minuteBucket returns the current 1-minute bucket number (Unix time / 60).
func minuteBucket() int64 {
	return time.Now().Unix() / 60
}
